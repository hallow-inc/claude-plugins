package hookio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const toolMissing = "golangci-lint 2.13.2 is pinned by mise.toml but is not on PATH"

func environmentAdapter(t *testing.T, junit string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":1,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true},"CODE-ZERO-WARNINGS":{"tool":"golangci-lint","fast":true}}}' ;;
run)
  case "$2" in
  CODE-ZERO-WARNINGS) printf '%s' '{"protocol":1,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + toolMissing + `"}}' ;;
  *) mkdir -p "$6"; printf '%s' '` + junit + `' > "$6/junit.xml"
     printf '%s' '{"protocol":1,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"go":"1"},"pinned_by":{}}' ;;
  esac ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

func environmentFixture(t *testing.T, junit string) string {
	t.Helper()
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", unbornManifest)
	writeFile(t, root, ".gitignore", ".assure/state/\n")
	writeFile(t, root, "p/x.xx", "one\n")
	environmentAdapter(t, junit)
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "init")
	hook("session-start", payload(t, "session-start-startup", root, nil))
	writeFile(t, root, "p/x.xx", "two\n")
	return root
}

func TestStopToolEnvironmentIsOutsideReachUnlessMixed(t *testing.T) {
	root := environmentFixture(t, `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`)
	code, out := stopWith(t, root, false)
	msg, _ := out["systemMessage"].(string)
	if code != 0 || out["decision"] != nil || !strings.Contains(msg, toolMissing) || !strings.Contains(msg, "assure tools --install-script | sh") {
		t.Fatalf("only environment: got %d %v; a missing tool is the human's to install, so the stop is allowed with the message and the install hint", code, out)
	}

	root = environmentFixture(t, `<testsuite name="p" tests="1" failures="1"><testcase classname="p" name="TestBrokenWidget"><failure message="boom">boom</failure></testcase></testsuite>`)
	code, out = stopWith(t, root, false)
	reason, _ := out["reason"].(string)
	if code != 2 || out["decision"] != "block" || !strings.Contains(reason, "TestBrokenWidget") || !strings.Contains(reason, toolMissing) {
		t.Fatalf("environment plus failing test: got %d %v; a failing test is the agent's to fix, so the stop blocks and names both failures", code, out)
	}
}
