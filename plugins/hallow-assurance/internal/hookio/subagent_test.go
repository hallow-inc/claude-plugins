package hookio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

const formerInspector = "hallow-assurance:inspector"

func TestSubagentStopIgnoresOtherAgents(t *testing.T) {
	root := fixture(t)
	base := subagentBase(t, root)
	nearMiss := []string{"", "verifier", "inspector", "general-purpose", core.Implementer, core.Pruner, formerInspector,
		"Hallow-Assurance:verifier", core.Verifier + " ", " " + core.Verifier, "x-" + core.Verifier, core.Verifier + "\n"}
	agentType := rapid.OneOf(rapid.SampledFrom(nearMiss), rapid.StringMatching(`hallow-assurance:[a-z]{0,10}`), rapid.String()).
		Filter(func(s string) bool { return s != core.Verifier })
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
			rt.Fatalf("agent_type %q: got exit %d, stdout %q; only the verifier is checked", set["agent_type"], code, out.String())
		}
	})
	if states, _ := filepath.Glob(filepath.Join(root, ".assure", "state", "subagent-stop-*")); len(states) != 0 {
		t.Fatalf("an ignored subagent left retry state: %v", states)
	}
}

func TestFormerInspectorStopIsIgnored(t *testing.T) {
	root := fixture(t)
	valid := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"` + formerInspector + `"}},"results":[]}]}`
	in := subagentInput(t, subagentBase(t, root), map[string]any{"agent_type": formerInspector, "last_assistant_message": "Done.\n```sarif\n" + valid + "\n```\n"})
	code, out, _ := hook("subagent-stop", in)
	if code != 0 || out != nil {
		t.Fatalf("retired inspector stop: got %d %v; an older plugin may still fire it, and the hook must neither block nor report", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".assure", "state", "inspections")); err == nil {
		t.Fatal("retired inspector stop created .assure/state/inspections/; nothing reads it any more")
	}
}

func TestVerifierStopBlocksNamingTheFailingTestPerAgent(t *testing.T) {
	root := goFixture(t)
	writeFile(t, root, "p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) { t.Fatal(\"one is not two\") }\n")
	base := subagentBase(t, root)
	stops := map[string]int{"verA1": core.StopCap + 1, "verB2": 1}
	for _, agent := range []string{"verA1", "verB2"} {
		for i := 1; i <= stops[agent]; i++ {
			code, out, _ := hook("subagent-stop", subagentInput(t, base, map[string]any{"agent_type": core.Verifier, "agent_id": agent, "stop_hook_active": nil}))
			reason, _ := out["reason"].(string)
			msg, _ := out["systemMessage"].(string)
			if i > core.StopCap {
				if code != 0 || out["decision"] != nil || !strings.Contains(msg, "after 3 attempts") {
					t.Fatalf("verifier %s stop %d: got %d %v; after the cap the verifier must be allowed to stop with a message", agent, i, code, out)
				}
				continue
			}
			if code != 2 || out["decision"] != "block" || !strings.Contains(reason, "TestOne") || !strings.Contains(reason, fmt.Sprintf("attempt %d of 3", i)) {
				t.Fatalf("verifier %s stop %d: got %d %v; each verifier's blocks are counted on its own, without stop_hook_active", agent, i, code, out)
			}
			data, err := os.ReadFile(subagentStatePath(root, session, agent))
			var s core.StopState
			if err != nil || json.Unmarshal(data, &s) != nil || s.Blocks != i {
				t.Fatalf("verifier %s state after stop %d: %q %v, want %d blocks", agent, i, data, err, i)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".assure", "state", "stop-"+session+".json")); err == nil {
		t.Fatal("a verifier's stop wrote the parent's Stop state")
	}
}

func TestSubagentStopBlocksOnlyWhatTheAgentCanFix(t *testing.T) {
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", "version: 0\nno_such_key: 1\n")
	_, loadErr := app.ManifestFor(root)
	if loadErr == nil {
		t.Fatal("fixture manifest loads; the case needs one that does not")
	}
	base := subagentBase(t, root)
	for i := range core.StopCap + 1 {
		code, out, _ := hook("subagent-stop", subagentInput(t, base, map[string]any{"agent_type": core.Verifier, "stop_hook_active": i > 0, "last_assistant_message": "no report"}))
		msg, _ := out["systemMessage"].(string)
		if code != 0 || out["decision"] != nil || !strings.Contains(msg, loadErr.Error()) {
			t.Fatalf("verifier stop %d with a broken manifest: got %d %v; the subagent may not edit assurance.yaml, so blocking only loops", i+1, code, out)
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
