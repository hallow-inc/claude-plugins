package hookio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const retryToolMissing = "t 1.0 is pinned but is not on PATH"

func retryAdapter(t *testing.T, mode string) {
	t.Helper()
	bin := t.TempDir()
	run := `mkdir -p "$6"; printf '%s' '<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>' > "$6/junit.xml"
     printf '%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"t":"1"},"pinned_by":{}}'`
	switch mode {
	case "fail":
		run = `mkdir -p "$6"; printf '%s' '<testsuite name="p" tests="1" failures="1"><testcase classname="p" name="TestBrokenWidget"><failure message="boom">boom</failure></testcase></testsuite>' > "$6/junit.xml"
     printf '%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"t":"1"},"pinned_by":{}}'`
	case "environment":
		run = `printf '%s' '{"protocol":2,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + retryToolMissing + `"}}'`
	}
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":2,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true}}}' ;;
run) ` + run + ` ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

func unrecordableStop(t *testing.T, mode string) (int, map[string]any) {
	t.Helper()
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", unbornManifest)
	writeFile(t, root, ".gitignore", ".assure/state/\n")
	writeFile(t, root, "p/x.xx", "one\n")
	retryAdapter(t, mode)
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "init")
	hook("session-start", payload(t, "session-start-startup", root, nil))
	writeFile(t, root, "p/x.xx", "two\n")

	in := payload(t, "stop-first", root, nil)
	state := filepath.Join(root, core.StateDir, "stop-"+session+".json")
	writeFile(t, state, "occupied", "the retry count cannot be renamed over a non-empty directory\n")
	code, out, _ := hook("stop", in)
	return code, out
}

func TestUnrecordableRetryCountIsReported(t *testing.T) {
	const warning = "could not record the retry count"
	modes := []struct {
		mode, why string
		ok        func(code int, out map[string]any, reason, msg string) bool
	}{
		{"fail", "a block whose count is lost may repeat past the cap, so the block itself must say the count was not recorded",
			func(code int, out map[string]any, reason, msg string) bool {
				return code == 2 && out["decision"] == "block" && strings.Contains(reason, "TestBrokenWidget") && strings.Contains(reason, warning) && msg == ""
			}},
		{"environment", "an allowed stop reports through systemMessage, so the lost count must be reported there",
			func(code int, out map[string]any, reason, msg string) bool {
				return code == 0 && out["decision"] == nil && strings.Contains(msg, retryToolMissing) && strings.Contains(msg, warning) && reason == ""
			}},
		{"pass", "a passing stop has nothing to warn about, and the lost count must not turn it into a block",
			func(code int, out map[string]any, _, _ string) bool { return code == 0 && out == nil }},
	}
	for _, m := range modes {
		t.Run(m.mode, func(t *testing.T) {
			code, out := unrecordableStop(t, m.mode)
			reason, _ := out["reason"].(string)
			msg, _ := out["systemMessage"].(string)
			if !m.ok(code, out, reason, msg) {
				t.Fatalf("got %d %v; %s", code, out, m.why)
			}
		})
	}
}
