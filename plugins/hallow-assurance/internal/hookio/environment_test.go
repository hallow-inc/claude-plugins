package hookio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const toolMissing = "vitest is not installed in node_modules; run npm ci"

const environmentManifest = "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: B\ncomponents: []\n"

func environmentAdapter(t *testing.T, junit string) {
	t.Helper()
	bin := t.TempDir()
	describe := func(lang string) string {
		return `printf '%s' '{"protocol":2,"languages":["` + lang + `"],"claims":["**/*.` + lang + `"],"patterns":{"test":["**/*_test.` + lang + `"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true}}}'`
	}
	scripts := map[string]string{
		"xx": `run) mkdir -p "$6"; printf '%s' '` + junit + `' > "$6/junit.xml"
     printf '%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"go":"1"},"pinned_by":{}}' ;;`,
		"yy": `run) printf '%s' '{"protocol":2,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + toolMissing + `"}}' ;;`,
	}
	for lang, run := range scripts {
		script := "#!/bin/sh\ncase \"$1\" in\ndescribe) " + describe(lang) + " ;;\n" + run + "\n*) echo \"assure-adapter-" + lang + ": unexpected $*\" >&2; exit 1 ;;\nesac\n"
		if err := os.WriteFile(filepath.Join(bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

func environmentFixture(t *testing.T, junit string) string {
	t.Helper()
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", environmentManifest)
	writeFile(t, root, ".gitignore", ".assure/state/\n")
	writeFile(t, root, "p/x.xx", "one\n")
	writeFile(t, root, "w/y.yy", "one\n")
	environmentAdapter(t, junit)
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "init")
	hook("session-start", payload(t, "session-start-startup", root, nil))
	writeFile(t, root, "p/x.xx", "two\n")
	writeFile(t, root, "w/y.yy", "two\n")
	return root
}

func TestStopToolEnvironmentIsOutsideReachUnlessMixed(t *testing.T) {
	root := environmentFixture(t, `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`)
	code, out := stopWith(t, root, false)
	msg, _ := out["systemMessage"].(string)
	if code != 0 || out["decision"] != nil || !strings.Contains(msg, toolMissing) || !strings.Contains(msg, "assure tools --install-script | sh") {
		t.Fatalf("only the yy VER-TESTS-PASS environment fails: got %d %v; a missing tool is the human's to install, so the stop is allowed with the message and the install hint", code, out)
	}

	root = environmentFixture(t, `<testsuite name="p" tests="1" failures="1"><testcase classname="p" name="TestBrokenWidget"><failure message="boom">boom</failure></testcase></testsuite>`)
	code, out = stopWith(t, root, false)
	reason, _ := out["reason"].(string)
	if code != 2 || out["decision"] != "block" || !strings.Contains(reason, "TestBrokenWidget") || !strings.Contains(reason, toolMissing) {
		t.Fatalf("yy environment plus a failing xx test: got %d %v; a failing test is the agent's to fix, so the stop blocks and names both failures", code, out)
	}
}
