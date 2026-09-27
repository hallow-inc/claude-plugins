package hallowassurance_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func shim(t *testing.T, dir, path, stdin string) (int, string, string) {
	t.Helper()
	abs, err := filepath.Abs("plugin/bin/assure-hook")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", abs, "stop")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + path, "PWD=" + dir}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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
	code, _, errb := shim(t, adoptedDir(t), "/usr/bin:/bin", "{}")
	if code != 2 || !strings.Contains(errb, "not on PATH") || !strings.Contains(errb, "README") {
		t.Fatalf("got %d %q", code, errb)
	}
}

func TestShimBlocksOnProtocolMismatch(t *testing.T) {
	path := fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && echo 7`)
	code, _, errb := shim(t, adoptedDir(t), path, "{}")
	if code != 2 || !strings.Contains(errb, "protocol 0") || !strings.Contains(errb, "'7'") {
		t.Fatalf("got %d %q", code, errb)
	}
}

func TestShimExecsAssureWithStdin(t *testing.T) {
	path := fakeAssure(t, `if [ "$1 $2" = "hook protocol" ]; then echo 0; exit 0; fi
echo "args=$*"; cat; exit 2`)
	code, out, _ := shim(t, adoptedDir(t), path, `{"hook_event_name":"Stop"}`)
	if code != 2 || out != "args=hook stop\n{\"hook_event_name\":\"Stop\"}" {
		t.Fatalf("got %d %q", code, out)
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
		"Stop":         {"", "stop", 180},
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
