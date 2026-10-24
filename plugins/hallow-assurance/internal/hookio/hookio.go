package hookio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const Protocol = 3

const (
	maxInput   = 16 << 20
	maxMessage = 9000
	pruneAge   = 14 * 24 * time.Hour
)

var eventNames = map[string]string{
	"session-start": "SessionStart",
	"stop":          "Stop",
}

type input struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	StopHookActive *bool  `json:"stop_hook_active"`
}

type specific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
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
	if err == nil {
		err = validate(event, in)
	}
	if err != nil {
		return failure(event, err)
	}
	if event == "session-start" {
		return sessionStart(in)
	}
	return stop(in)
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
	if !core.ValidSession(in.SessionID) {
		return fmt.Errorf("hook input session_id %q is not a valid session id", in.SessionID)
	}
	return nil
}

func failure(event string, err error) response {
	msg := "assure hook " + event + " failed closed: " + err.Error()
	if event == "stop" {
		return response{out: &output{Decision: "block", Reason: msg}, code: 2}
	}
	return response{out: &output{HookSpecificOutput: &specific{HookEventName: "SessionStart", AdditionalContext: msg}}}
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
		s.AdditionalContext = capMessage(s.AdditionalContext)
		o.HookSpecificOutput = &s
	}
	data, err := json.Marshal(o)
	if err == nil {
		_, err = stdout.Write(append(data, '\n'))
	}
	if resp.code == 2 {
		_, _ = fmt.Fprintln(stderr, o.Reason)
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
	text := core.RenderContext(m, nil)
	if len(notes) > 0 {
		text = strings.Join(notes, "\n") + "\n\n" + text
	}
	return response{out: &output{HookSpecificOutput: &specific{HookEventName: "SessionStart", AdditionalContext: text}}}
}

type stopInput struct {
	rep    app.Report
	active bool
}

func (in stopInput) check() core.StopCheck {
	passed := !in.rep.Blocking()
	return core.StopCheck{
		Passed:       passed,
		OutsideReach: !passed && in.rep.OutsideReach(),
		Fingerprint:  core.StopFingerprint(in.rep.Failures()),
	}
}

func (in stopInput) summary() string {
	s := in.rep.Render()
	if r := in.rep.Remedies(); len(r) > 0 {
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
			"assure fast check failed (attempt %d of %d). Fix these before stopping:\n\n%s", next.Blocks, core.StopCap, in.summary())}, code: 2}
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
		return manifestUnavailable(err)
	}
	si := stopInput{rep: app.FastCheck(m, "HEAD", in.SessionID), active: in.StopHookActive != nil && *in.StopHookActive}
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

func manifestUnavailable(err error) response {
	return response{out: &output{SystemMessage: "assure: the Stop check cannot run, because the manifest does not load; the agent was allowed to stop: " + err.Error()}}
}
