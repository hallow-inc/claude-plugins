package hookio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const Protocol = 2

var HarnessProtected = []string{".claude/settings.json", ".claude/settings.local.json"}

const (
	maxInput   = 16 << 20
	maxMessage = 9000
	pruneAge   = 14 * 24 * time.Hour
)

var eventNames = map[string]string{
	"session-start": "SessionStart",
	"pre-tool-use":  "PreToolUse",
	"post-tool-use": "PostToolUse",
	"stop":          "Stop",
	"subagent-stop": "SubagentStop",
}

var guardedTools = map[string]bool{"Edit": true, "Write": true, "NotebookEdit": true}

type input struct {
	SessionID      string  `json:"session_id"`
	Cwd            string  `json:"cwd"`
	HookEventName  string  `json:"hook_event_name"`
	AgentID        string  `json:"agent_id"`
	AgentType      string  `json:"agent_type"`
	ToolUseID      string  `json:"tool_use_id"`
	ToolName       string  `json:"tool_name"`
	StopHookActive *bool   `json:"stop_hook_active"`
	LastMessage    *string `json:"last_assistant_message"`
	ToolInput      struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

type specific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

type output struct {
	Decision           string    `json:"decision,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	SystemMessage      string    `json:"systemMessage,omitempty"`
	HookSpecificOutput *specific `json:"hookSpecificOutput,omitempty"`
}

type response struct {
	out  *output
	code int
}

func IsEvent(event string) bool {
	_, ok := eventNames[event]
	return ok
}

func Run(event string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	var resp response
	defer func() {
		if r := recover(); r != nil {
			resp = failure(event, fmt.Errorf("internal error: %v", r))
		}
		code = write(resp, stdout, stderr)
	}()
	resp = handler(event, stdin)
	return 0
}

var handler = handle

func handle(event string, stdin io.Reader) response {
	in, err := parse(stdin)
	if err == nil && event == "subagent-stop" && !roleAgents[in.AgentType] {
		return response{}
	}
	if err == nil {
		err = validate(event, in)
	}
	if err != nil {
		return failure(event, err)
	}
	switch event {
	case "subagent-stop":
		return subagentStop(in)
	case "pre-tool-use":
		return preToolUse(in)
	case "post-tool-use":
		return postToolUse(in)
	case "session-start":
		return sessionStart(in)
	default:
		return stop(in)
	}
}

func parse(stdin io.Reader) (input, error) {
	var in input
	data, err := io.ReadAll(io.LimitReader(stdin, maxInput+1))
	if err != nil {
		return in, fmt.Errorf("reading hook input: %w", err)
	}
	if len(data) > maxInput {
		return in, fmt.Errorf("hook input exceeds %d bytes", maxInput)
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return in, fmt.Errorf("decoding hook input: %w", err)
	}
	return in, nil
}

func validate(event string, in input) error {
	if want := eventNames[event]; in.HookEventName != want {
		return fmt.Errorf("hook input is for %q, not %s", in.HookEventName, want)
	}
	if in.Cwd == "" || !filepath.IsAbs(in.Cwd) {
		return errors.New("hook input has no absolute cwd")
	}
	if event != "pre-tool-use" && !core.ValidSession(in.SessionID) {
		return fmt.Errorf("hook input session_id %q is not a valid session id", in.SessionID)
	}
	if event == "subagent-stop" && !core.ValidSession(in.AgentID) {
		return fmt.Errorf("hook input agent_id %q is not a valid agent id", in.AgentID)
	}
	return nil
}

func failure(event string, err error) response {
	msg := "assure hook " + event + " failed closed: " + err.Error()
	switch event {
	case "pre-tool-use":
		return deny(msg)
	case "stop", "subagent-stop":
		return response{out: &output{Decision: "block", Reason: msg}, code: 2}
	case "post-tool-use":
		return gapMessage(msg)
	default:
		return response{out: &output{HookSpecificOutput: &specific{HookEventName: "SessionStart", AdditionalContext: msg}}}
	}
}

func deny(reason string) response {
	return response{out: &output{HookSpecificOutput: &specific{
		HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: reason,
	}}, code: 2}
}

func capMessage(s string) string {
	if len(s) <= maxMessage {
		return s
	}
	return s[:maxMessage] + "\n… truncated"
}

func write(resp response, stdout, stderr io.Writer) int {
	if resp.out == nil {
		return resp.code
	}
	o := *resp.out
	o.Reason, o.SystemMessage = capMessage(o.Reason), capMessage(o.SystemMessage)
	if o.HookSpecificOutput != nil {
		s := *o.HookSpecificOutput
		s.PermissionDecisionReason, s.AdditionalContext = capMessage(s.PermissionDecisionReason), capMessage(s.AdditionalContext)
		o.HookSpecificOutput = &s
	}
	data, err := json.Marshal(o)
	if err == nil {
		_, err = stdout.Write(append(data, '\n'))
	}
	if resp.code == 2 {
		reason := o.Reason
		if o.HookSpecificOutput != nil && reason == "" {
			reason = o.HookSpecificOutput.PermissionDecisionReason
		}
		_, _ = fmt.Fprintln(stderr, reason)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure hook: writing output: %v\n", err)
		return 2
	}
	return resp.code
}

func adopted(dir string) (*core.Manifest, bool, error) {
	if _, err := core.FindManifest(dir); errors.Is(err, core.ErrNoManifest) {
		return nil, false, nil
	}
	m, err := app.ManifestFor(dir)
	return m, true, err
}

func extraGlobs() []core.Glob {
	var gs []core.Glob
	for _, s := range HarnessProtected {
		g, err := core.CompileGlob(s)
		if err != nil {
			panic(err)
		}
		gs = append(gs, g)
	}
	return gs
}

func (in input) target() string {
	p := in.ToolInput.FilePath
	if in.ToolName == "NotebookEdit" {
		p = in.ToolInput.NotebookPath
	}
	if p != "" && !filepath.IsAbs(p) {
		p = filepath.Join(in.Cwd, p)
	}
	return p
}

func preToolUse(in input) response {
	if !guardedTools[in.ToolName] {
		return response{}
	}
	p := in.target()
	if p == "" {
		return deny(fmt.Sprintf("%s input has no file path", in.ToolName))
	}
	m, ok, _ := adopted(filepath.Dir(p))
	if !ok {
		return response{}
	}
	g, err := app.NewGuard(HarnessProtected)
	if err != nil {
		return deny(err.Error())
	}
	if ok, reason := g.Check(p, in.AgentType); !ok {
		return deny(reason)
	}
	if m != nil {
		if rel, inside := m.Rel(p); inside {
			_ = app.WritePending(m, in.SessionID, in.ToolUseID, rel)
		}
	}
	return response{}
}

func gapMessage(msg string) response {
	return response{out: &output{SystemMessage: msg + "; CI will count this edit as a provenance gap"}}
}

func postToolUse(in input) response {
	if !guardedTools[in.ToolName] {
		return response{}
	}
	p := in.target()
	dir := filepath.Dir(p)
	if p == "" {
		dir = in.Cwd
	}
	m, ok, err := adopted(dir)
	if !ok {
		return response{}
	}
	if err != nil {
		return gapMessage("assure record: " + err.Error())
	}
	if !m.Provenance {
		return response{}
	}
	if p == "" {
		return gapMessage(fmt.Sprintf("assure record: %s input has no file path", in.ToolName))
	}
	rel, inside := m.Rel(p)
	if !inside {
		return gapMessage("assure record: " + p + " is outside the manifest root " + m.Root)
	}
	if _, _, err := app.Record(m, app.RecordInput{Session: in.SessionID, Tool: in.ToolName, ToolUseID: in.ToolUseID,
		AgentID: in.AgentID, AgentType: in.AgentType, Path: rel}); err != nil {
		return gapMessage(fmt.Sprintf("assure record: %s: %v", rel, err))
	}
	return response{}
}

func sessionStart(in input) response {
	m, ok, err := adopted(in.Cwd)
	if !ok {
		return response{}
	}
	if err != nil {
		return failure("session-start", err)
	}
	var notes []string
	_, failures, cacheErr := app.LoadRoles(m)
	for _, e := range append(failures, cacheErr) {
		if e != nil {
			notes = append(notes, "warning: "+e.Error())
		}
	}
	core.PruneState(m.Root, pruneAge, time.Now())
	if _, err := os.Stat(core.SnapshotPath(m.Root, in.SessionID)); errors.Is(err, os.ErrNotExist) {
		s, err := core.TakeSnapshot(m, in.SessionID, extraGlobs())
		if err == nil {
			err = core.WriteSnapshot(m, s)
		}
		if err != nil {
			notes = append(notes, "warning: protected-file snapshot failed; the Stop check will block: "+err.Error())
		}
	}
	text := core.RenderContext(m, nil)
	if len(notes) > 0 {
		text = strings.Join(notes, "\n") + "\n\n" + text
	}
	return response{out: &output{HookSpecificOutput: &specific{HookEventName: "SessionStart", AdditionalContext: text}}}
}

func drift(m *core.Manifest, session string) ([]string, error) {
	s, err := core.ReadSnapshot(m, session)
	if err != nil {
		return nil, err
	}
	return core.Drift(m, s, extraGlobs())
}

type stopInput struct {
	name     string
	rep      app.Report
	drifted  []string
	driftErr error
	active   bool
}

func (in stopInput) check() core.StopCheck {
	failures := in.rep.Failures()
	switch {
	case in.driftErr == nil:
	case core.OutsideReach(in.driftErr):
		failures = append(failures, core.FailureKey{Objective: "drift", Keys: []string{"snapshot-missing"}})
	default:
		failures = append(failures, core.FailureKey{Objective: "drift", Keys: []string{"drift-error"}})
	}
	passed := !in.rep.Blocking() && len(in.drifted) == 0 && in.driftErr == nil
	return core.StopCheck{
		Passed:       passed,
		OutsideReach: !passed && in.rep.OutsideReach() && len(in.drifted) == 0 && (in.driftErr == nil || core.OutsideReach(in.driftErr)),
		Fingerprint:  core.StopFingerprint(failures, in.drifted),
	}
}

func (in stopInput) remedies() []string {
	out := in.rep.Remedies()
	if core.OutsideReach(in.driftErr) {
		out = append(out, "restart Claude Code: this session has no protected-file snapshot, so drift cannot be checked")
	}
	return out
}

func (in stopInput) summary() string {
	s := in.rep.Render()
	var lines []string
	for _, d := range in.drifted {
		lines = append(lines, "protected file "+d+" since session start (a human must review or revert it)")
	}
	if in.driftErr != nil {
		lines = append(lines, "drift cannot be ruled out: "+in.driftErr.Error())
	}
	if len(lines) > 0 {
		s += "\nProtected-file drift:\n  - " + strings.Join(lines, "\n  - ") + "\n"
	}
	if r := in.remedies(); len(r) > 0 {
		s += "\nThe agent cannot fix these; a human must:\n  - " + strings.Join(r, "\n  - ") + "\n"
	}
	return s
}

func decideStop(in stopInput, state core.StopState) (core.StopState, response) {
	next, action := core.NextStop(state, in.active, in.check())
	switch action {
	case core.StopAllow:
		return next, response{}
	case core.StopBlock:
		return next, response{out: &output{Decision: "block", Reason: fmt.Sprintf(
			"assure %s failed (attempt %d of %d). Fix these before stopping:\n\n%s", in.name, next.Blocks, core.StopCap, in.summary())}, code: 2}
	case core.StopOutsideReach:
		return next, response{out: &output{SystemMessage: "assure: checks cannot run in this session and the agent cannot fix that; the agent was allowed to stop. Review before merging:\n\n" + in.summary()}}
	default:
		return next, response{out: &output{SystemMessage: fmt.Sprintf(
			"assure: checks still fail after %d attempts; the agent was allowed to stop. Review before merging:\n\n%s", core.StopCap, in.summary())}}
	}
}

func stop(in input) response {
	m, ok, err := adopted(in.Cwd)
	if !ok {
		return response{}
	}
	if err != nil {
		return manifestUnavailable("Stop", err)
	}
	si := stopInput{name: "fast check", rep: app.FastCheck(m, "HEAD", in.SessionID), active: in.StopHookActive != nil && *in.StopHookActive}
	si.drifted, si.driftErr = drift(m, in.SessionID)
	next, resp := decideStop(si, core.ReadStopState(m.Root, in.SessionID))
	if err := core.WriteStopState(m.Root, in.SessionID, next); err != nil && resp.out != nil {
		o := *resp.out
		note := "\nwarning: could not record the retry count: " + err.Error() + "\n"
		if o.Decision == "block" {
			o.Reason += note
		} else {
			o.SystemMessage += note
		}
		resp.out = &o
	}
	return resp
}
