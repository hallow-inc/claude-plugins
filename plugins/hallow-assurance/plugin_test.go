package hallowassurance_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func shim(t *testing.T, dir, path, stdin string) (int, string, string) {
	t.Helper()
	return shimEvent(t, "stop", dir, path, stdin)
}

func shimEvent(t *testing.T, event, dir, path, stdin string) (int, string, string) {
	t.Helper()
	abs, err := filepath.Abs("plugin/bin/assure-hook")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", abs, event)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + path, "PWD=" + dir}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func fakeAssure(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "assure"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + ":/usr/bin:/bin"
}

func adoptedDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assurance.yaml"), []byte("version: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestShimIsInertWithoutManifest(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if code, out, errb := shim(t, dir, "/usr/bin:/bin", "{}"); code != 0 || out != "" || errb != "" {
		t.Fatalf("got %d %q %q", code, out, errb)
	}
}

func TestShimBlocksWhenAssureMissing(t *testing.T) {
	code, _, errb := shimEvent(t, "pre-tool-use", adoptedDir(t), "/usr/bin:/bin", "{}")
	if code != 2 || !strings.Contains(errb, "not on PATH") || !strings.Contains(errb, "README") {
		t.Fatalf("got %d %q", code, errb)
	}
}

func TestShimBlocksOnProtocolMismatch(t *testing.T) {
	path := fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && echo 7`)
	code, _, errb := shimEvent(t, "session-start", adoptedDir(t), path, "{}")
	if code != 2 || !strings.Contains(errb, "protocol 2") || !strings.Contains(errb, "'7'") {
		t.Fatalf("got %d %q", code, errb)
	}
}

func TestShimExecsAssureWithStdin(t *testing.T) {
	path := fakeAssure(t, `if [ "$1 $2" = "hook protocol" ]; then echo 2; exit 0; fi
echo "args=$*"; cat; exit 2`)
	for _, event := range []string{"stop", "subagent-stop"} {
		code, out, _ := shimEvent(t, event, adoptedDir(t), path, `{"hook_event_name":"X"}`)
		if code != 2 || out != "args=hook "+event+"\n{\"hook_event_name\":\"X\"}" {
			t.Fatalf("%s: got %d %q", event, code, out)
		}
	}
}

func TestHooksJSONDeclaresEveryHookWithATimeout(t *testing.T) {
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Timeout int      `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(read(t, "plugin/hooks/hooks.json"), &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		matcher, arg string
		timeout      int
	}{
		"SessionStart": {"", "session-start", 30},
		"PreToolUse":   {"Edit|Write|NotebookEdit", "pre-tool-use", 10},
		"PostToolUse":  {"Edit|Write|NotebookEdit", "post-tool-use", 10},
		"Stop":         {"", "stop", 180},
		"SubagentStop": {"^hallow-assurance:(verifier|inspector)$", "subagent-stop", 180},
	}
	if len(cfg.Hooks) != len(want) {
		t.Fatalf("events %v, want exactly %v", cfg.Hooks, want)
	}
	for event, w := range want {
		groups := cfg.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: want one hook, got %+v", event, groups)
		}
		h := groups[0].Hooks[0]
		if groups[0].Matcher != w.matcher || h.Type != "command" || h.Command != "${CLAUDE_PLUGIN_ROOT}/bin/assure-hook" ||
			len(h.Args) != 1 || h.Args[0] != w.arg || h.Timeout != w.timeout {
			t.Errorf("%s: got matcher %q %+v; a hook without an explicit timeout waits 600 s and then fails open", event, groups[0].Matcher, h)
		}
	}
	if pre, post := cfg.Hooks["PreToolUse"], cfg.Hooks["PostToolUse"]; len(pre) != 1 || len(post) != 1 || pre[0].Matcher != post[0].Matcher {
		t.Errorf("PostToolUse matcher must equal PreToolUse matcher: a tool the guard sees but record does not leaves every such edit a provenance gap")
	}
	checkSubagentMatcher(t, cfg.Hooks["SubagentStop"][0].Matcher)
}

func checkSubagentMatcher(t *testing.T, matcher string) {
	t.Helper()
	re, err := regexp.Compile(matcher)
	if err != nil {
		t.Fatalf("SubagentStop matcher %q: %v", matcher, err)
	}
	roles := []string{"hallow-assurance:verifier", "hallow-assurance:inspector"}
	near := rapid.SampledFrom(append([]string{"verifier", "inspector", "hallow-assurance:implementer", "hallow-assurance:pruner", "general-purpose"}, roles...))
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.StringMatching(`[a-z:-]{0,3}`).Draw(rt, "prefix") + near.Draw(rt, "agent") + rapid.StringMatching(`[a-z:-]{0,3}`).Draw(rt, "suffix")
		if re.MatchString(s) != slices.Contains(roles, s) {
			rt.Fatalf("matcher %q on %q: matched=%v; an unanchored matcher runs role checks on other subagents", matcher, s, re.MatchString(s))
		}
	})
	for _, s := range []string{"verifier", "hallow-assurance:implementer", "x-hallow-assurance:verifier"} {
		if re.MatchString(s) {
			t.Errorf("SubagentStop matcher matches %q", s)
		}
	}
}

func TestMarketplaceListsThePlugin(t *testing.T) {
	var mk struct {
		Plugins []struct{ Name, Source string } `json:"plugins"`
	}
	if err := json.Unmarshal(read(t, "../../.claude-plugin/marketplace.json"), &mk); err != nil {
		t.Fatal(err)
	}
	for _, p := range mk.Plugins {
		if p.Name == "hallow-assurance" && p.Source == "./plugins/hallow-assurance/plugin" {
			return
		}
	}
	t.Fatalf("marketplace has no hallow-assurance entry pointing at ./plugins/hallow-assurance/plugin: %+v", mk.Plugins)
}

func stopMessage(t *testing.T, code int, out string) string {
	t.Helper()
	var o struct {
		SystemMessage string `json:"systemMessage"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &o) != nil || o.SystemMessage == "" {
		t.Fatalf("stop must allow with a JSON systemMessage: got %d %q", code, out)
	}
	return o.SystemMessage
}

func TestShimAllowsStopWhenAssureMissing(t *testing.T) {
	for _, event := range []string{"stop", "subagent-stop"} {
		code, out, _ := shimEvent(t, event, adoptedDir(t), "/usr/bin:/bin", "{}")
		if msg := stopMessage(t, code, out); !strings.Contains(msg, "not on PATH") || !strings.Contains(msg, "README") {
			t.Fatalf("%s: message %q does not say how to install assure", event, msg)
		}
	}
}

func TestShimAllowsStopOnProtocolMismatch(t *testing.T) {
	path := fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && echo 7`)
	for _, event := range []string{"stop", "subagent-stop"} {
		code, out, _ := shimEvent(t, event, adoptedDir(t), path, "{}")
		if msg := stopMessage(t, code, out); !strings.Contains(msg, "protocol 2") || !strings.Contains(msg, "speaks 7") {
			t.Fatalf("%s: message %q does not name both protocols", event, msg)
		}
	}
}

func TestShimStopMessageSurvivesHostileProtocolOutput(t *testing.T) {
	path := fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && printf '"\n{\\'`)
	code, out, _ := shim(t, adoptedDir(t), path, "{}")
	if msg := stopMessage(t, code, out); !strings.Contains(msg, "speaks unknown") {
		t.Fatalf("message %q", msg)
	}
}
