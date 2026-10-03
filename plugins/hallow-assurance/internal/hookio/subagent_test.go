package hookio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"pgregory.net/rapid"
)

type subagentTB interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

func subagentBase(t testing.TB, root string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(payload(t, "subagent-stop", root, nil), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func subagentInput(t subagentTB, base map[string]any, set map[string]any) []byte {
	t.Helper()
	m := map[string]any{}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range set {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func subagentStatePath(root, sess, agent string) string {
	return filepath.Join(root, ".assure", "state", "subagent-stop-"+sess+"-"+agent+".json")
}

func inspectionFile(root, sess, agent string) string {
	return filepath.Join(root, ".assure", "state", "inspections", sess+"-"+agent+".sarif")
}

type genResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []genLocation `json:"locations"`
}

type genLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			StartLine int `json:"startLine"`
		} `json:"region"`
	} `json:"physicalLocation"`
}

func sarifGen(t *rapid.T) ([]byte, map[string]int) {
	counts := map[string]int{}
	results := []genResult{}
	for range rapid.IntRange(0, 5).Draw(t, "results") {
		var r genResult
		r.RuleID = rapid.StringMatching(`[A-Z][A-Z-]{0,12}`).Draw(t, "rule")
		r.Level = rapid.SampledFrom([]string{"error", "warning", "note"}).Draw(t, "level")
		r.Message.Text = rapid.StringMatching(`[a-z][a-z ]{0,20}`).Draw(t, "text")
		for range rapid.IntRange(1, 2).Draw(t, "locations") {
			var l genLocation
			l.PhysicalLocation.ArtifactLocation.URI = rapid.StringMatching(`[a-z]{1,6}/[a-z]{1,6}\.txt`).Draw(t, "uri")
			l.PhysicalLocation.Region.StartLine = rapid.IntRange(1, 5000).Draw(t, "line")
			r.Locations = append(r.Locations, l)
		}
		counts[r.Level]++
		results = append(results, r)
	}
	log := map[string]any{
		"version": "2.1.0",
		"runs":    []any{map[string]any{"tool": map[string]any{"driver": map[string]any{"name": core.Inspector}}, "results": results}},
	}
	var data []byte
	var err error
	if rapid.Bool().Draw(t, "indent") {
		data, err = json.MarshalIndent(log, "", rapid.SampledFrom([]string{"  ", "\t", " "}).Draw(t, "indent with"))
	} else {
		data, err = json.Marshal(log)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data, counts
}

func sarifMessage(t *rapid.T, block []byte) string {
	prose := rapid.StringMatching(`[A-Za-z0-9 .,:]{0,30}(\n[A-Za-z0-9 .,:]{0,30}){0,3}`)
	return prose.Draw(t, "before") + "\n```sarif\n" + string(block) + "\n```\n" + prose.Draw(t, "after")
}

func TestSubagentStopIgnoresOtherAgents(t *testing.T) {
	root := fixture(t)
	base := subagentBase(t, root)
	nearMiss := []string{"", "verifier", "inspector", "general-purpose", core.Implementer, core.Pruner,
		"Hallow-Assurance:verifier", core.Verifier + " ", " " + core.Inspector, "x-" + core.Verifier, core.Inspector + "\n"}
	agentType := rapid.OneOf(rapid.SampledFrom(nearMiss), rapid.StringMatching(`hallow-assurance:[a-z]{0,10}`), rapid.String()).
		Filter(func(s string) bool { return s != core.Verifier && s != core.Inspector })
	str := rapid.OneOf(rapid.Just[any](nil), rapid.Map(rapid.String(), func(s string) any { return s }))
	flag := rapid.OneOf(rapid.Just[any](nil), rapid.Map(rapid.Bool(), func(b bool) any { return b }))
	rapid.Check(t, func(rt *rapid.T) {
		set := map[string]any{}
		if rapid.Bool().Draw(rt, "has agent_type") {
			set["agent_type"] = agentType.Draw(rt, "agent_type")
		} else {
			set["agent_type"] = nil
		}
		for _, k := range []string{"cwd", "session_id", "agent_id", "hook_event_name", "last_assistant_message"} {
			if rapid.Bool().Draw(rt, "replace "+k) {
				set[k] = str.Draw(rt, k)
			}
		}
		if rapid.Bool().Draw(rt, "replace stop_hook_active") {
			set["stop_hook_active"] = flag.Draw(rt, "stop_hook_active")
		}
		var out, errb bytes.Buffer
		code := Run("subagent-stop", bytes.NewReader(subagentInput(rt, base, set)), &out, &errb)
		if code != 0 || out.Len() != 0 {
			rt.Fatalf("agent_type %q: got exit %d, stdout %q; only the verifier and inspector are checked", set["agent_type"], code, out.String())
		}
	})
	if states, _ := filepath.Glob(filepath.Join(root, ".assure", "state", "subagent-stop-*")); len(states) != 0 {
		t.Fatalf("an ignored subagent left retry state: %v", states)
	}
}

func TestVerifierStopBlocksNamingTheFailingTestPerAgent(t *testing.T) {
	root := goFixture(t)
	writeFile(t, root, "p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) { t.Fatal(\"one is not two\") }\n")
	base := subagentBase(t, root)
	for _, agent := range []string{"verA1", "verB2"} {
		code, out, _ := hook("subagent-stop", subagentInput(t, base, map[string]any{"agent_type": core.Verifier, "agent_id": agent, "stop_hook_active": nil}))
		reason, _ := out["reason"].(string)
		if code != 2 || out["decision"] != "block" || !strings.Contains(reason, "TestOne") || !strings.Contains(reason, "attempt 1 of 3") {
			t.Fatalf("verifier %s: got %d %v; a parallel verifier's blocks must not count against another", agent, code, out)
		}
		data, err := os.ReadFile(subagentStatePath(root, session, agent))
		var s core.StopState
		if err != nil || json.Unmarshal(data, &s) != nil || s.Blocks != 1 {
			t.Fatalf("verifier %s state: %q %v, want one block", agent, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".assure", "state", "stop-"+session+".json")); err == nil {
		t.Fatal("a verifier's stop wrote the parent's Stop state")
	}
}

func TestSubagentStopBlocksOnlyWhatTheAgentCanFix(t *testing.T) {
	checkBrokenManifestAllows(t)
	checkMissingSARIFBlocks(t)
}

func checkBrokenManifestAllows(t *testing.T) {
	t.Helper()
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", "version: 0\nno_such_key: 1\n")
	_, loadErr := app.ManifestFor(root)
	if loadErr == nil {
		t.Fatal("fixture manifest loads; the case needs one that does not")
	}
	base := subagentBase(t, root)
	for _, agent := range []string{core.Verifier, core.Inspector} {
		for i := range core.StopCap + 1 {
			code, out, _ := hook("subagent-stop", subagentInput(t, base, map[string]any{"agent_type": agent, "stop_hook_active": i > 0, "last_assistant_message": "no report"}))
			msg, _ := out["systemMessage"].(string)
			if code != 0 || out["decision"] != nil || !strings.Contains(msg, loadErr.Error()) {
				t.Fatalf("%s stop %d with a broken manifest: got %d %v; the subagent may not edit assurance.yaml, so blocking only loops", agent, i+1, code, out)
			}
		}
	}
	for i := range core.StopCap + 2 {
		code, out := stopWith(t, root, i > 0)
		msg, _ := out["systemMessage"].(string)
		if code != 0 || out["decision"] != nil || !strings.Contains(msg, loadErr.Error()) {
			t.Fatalf("main-thread stop %d with a broken manifest: got %d %v; agents may not edit assurance.yaml and the retry cap's state lives under the manifest root, so a block repeats uncapped", i+1, code, out)
		}
	}
}

func checkMissingSARIFBlocks(t *testing.T) {
	t.Helper()
	root := fixture(t)
	base := subagentBase(t, root)
	valid := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"hallow-assurance:inspector"}},"results":[]}]}`
	cases := []struct {
		agent   string
		message any
		names   string
	}{
		{"nomsg", nil, "last_assistant_message"},
		{"prose", "I found nothing worth reporting.", "no "},
		{"jsonfence", "Findings:\n```json\n" + valid + "\n```\n", "no "},
		{"twoblocks", "```sarif\n" + valid + "\n```\n```sarif\n" + valid + "\n```\n", "2 "},
		{"wrongtool", "```sarif\n" + strings.Replace(valid, core.Inspector, "golangci-lint", 1) + "\n```\n", "/runs/0/tool/driver/name"},
		{"notjson", "```sarif\n{\"runs\":\n```\n", "not JSON"},
	}
	for _, c := range cases {
		code, out, _ := hook("subagent-stop", subagentInput(t, base, map[string]any{"agent_type": core.Inspector, "agent_id": c.agent, "last_assistant_message": c.message}))
		reason, _ := out["reason"].(string)
		if code != 2 || out["decision"] != "block" || !strings.Contains(reason, "```sarif") || !strings.Contains(reason, c.names) {
			t.Errorf("%s: got %d %v; want a block stating the fence and %q", c.agent, code, out, c.names)
		}
		if _, err := os.Stat(inspectionFile(root, session, c.agent)); err == nil {
			t.Errorf("%s: an inspection file was written for a rejected report", c.agent)
		}
	}
}

func TestInspectorStopWritesTheExactSARIFBytes(t *testing.T) {
	root := fixture(t)
	base := subagentBase(t, root)
	n := 0
	rapid.Check(t, func(rt *rapid.T) {
		n++
		agent := fmt.Sprintf("insp%d", n)
		block, counts := sarifGen(rt)
		code, out, _ := hook("subagent-stop", subagentInput(rt, base, map[string]any{"agent_type": core.Inspector, "agent_id": agent, "last_assistant_message": sarifMessage(rt, block)}))
		msg, _ := out["systemMessage"].(string)
		if code != 0 || out["decision"] != nil || !strings.Contains(msg, filepath.Join(".assure", "state", "inspections", session+"-"+agent+".sarif")) {
			rt.Fatalf("valid report: got %d %v; findings, even errors, must not block the inspector", code, out)
		}
		for _, level := range []string{"error", "warning", "note"} {
			if !regexp.MustCompile(fmt.Sprintf(`(^|\D)%d %s`, counts[level], level)).MatchString(msg) {
				rt.Fatalf("message %q does not count %d %s", msg, counts[level], level)
			}
		}
		got, err := os.ReadFile(inspectionFile(root, session, agent))
		if err != nil || !bytes.Equal(got, block) {
			rt.Fatalf("inspection file %q (%v), want the block's bytes %q", got, err, block)
		}
	})
}

type capModel struct {
	root, sess, agent   string
	sinceValid, counter int
}

func (c *capModel) stop(rt *rapid.T, base map[string]any) {
	good := rapid.IntRange(0, 4).Draw(rt, "outcome") == 0
	active := rapid.SampledFrom([]string{"absent", "true", "false"}).Draw(rt, "stop_hook_active")
	set := map[string]any{"agent_type": core.Inspector, "agent_id": c.agent, "session_id": c.sess, "last_assistant_message": "no report",
		"stop_hook_active": map[string]any{"absent": nil, "true": true, "false": false}[active]}
	var block []byte
	if good {
		block, _ = sarifGen(rt)
		set["last_assistant_message"] = sarifMessage(rt, block)
	}
	code, out, _ := hook("subagent-stop", subagentInput(rt, base, set))
	c.verdict(rt, block, active, code, out)
	data, err := os.ReadFile(subagentStatePath(c.root, c.sess, c.agent))
	var s core.StopState
	if err != nil || json.Unmarshal(data, &s) != nil || s.Blocks != c.counter {
		rt.Fatalf("%s: state %q %v, want %d consecutive blocks", c.agent, data, err, c.counter)
	}
}

func (c *capModel) verdict(rt *rapid.T, block []byte, active string, code int, out map[string]any) {
	good := block != nil
	reason, _ := out["reason"].(string)
	msg, _ := out["systemMessage"].(string)
	switch {
	case good:
		c.sinceValid, c.counter = 0, 0
		got, err := os.ReadFile(inspectionFile(c.root, c.sess, c.agent))
		if code != 0 || out["decision"] != nil || err != nil || !bytes.Equal(got, block) {
			rt.Fatalf("%s, valid report: got %d %v, file %q %v", c.agent, code, out, got, err)
		}
	case c.sinceValid >= core.StopCap:
		if code != 0 || out["decision"] != nil || !strings.Contains(msg, "after 3 attempts") {
			rt.Fatalf("%s: already blocked 3 times since its last valid report, got %d %v", c.agent, code, out)
		}
	default:
		if active == "false" {
			c.counter = 0
		}
		c.counter++
		c.sinceValid++
		want := fmt.Sprintf("attempt %d of 3", c.counter)
		if code != 2 || out["decision"] != "block" || !strings.Contains(reason, want) {
			rt.Fatalf("%s, stop_hook_active %s: got %d %v, want a block with %q", c.agent, active, code, out, want)
		}
	}
}

func TestSubagentStopRetryCapIsPerAgentAndSparesTheParent(t *testing.T) {
	root := fixture(t)
	writeFile(t, root, ".assure/state/.keep", "")
	base := subagentBase(t, root)
	iteration := 0
	rapid.Check(t, func(rt *rapid.T) {
		iteration++
		sess := fmt.Sprintf("cap%d", iteration)
		parent := filepath.Join(root, ".assure", "state", "stop-"+sess+".json")
		parentBody := []byte(`{"version":1,"blocks":2}`)
		if err := os.WriteFile(parent, parentBody, 0o644); err != nil {
			rt.Fatal(err)
		}
		agents := rapid.SliceOfNDistinct(rapid.StringMatching(`[A-Za-z0-9_-]{1,20}`), 1, 3, rapid.ID[string]).Draw(rt, "agents")
		models := map[string]*capModel{}
		for _, a := range agents {
			models[a] = &capModel{root: root, sess: sess, agent: a}
		}
		for range rapid.IntRange(1, 14).Draw(rt, "stops") {
			models[rapid.SampledFrom(agents).Draw(rt, "agent")].stop(rt, base)
		}
		if got, err := os.ReadFile(parent); err != nil || !bytes.Equal(got, parentBody) {
			rt.Fatalf("parent Stop state changed to %q (%v)", got, err)
		}
	})
}
