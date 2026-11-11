package hookio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

func flakyCase(name string, rerun int) string {
	props := ""
	if rerun > 0 {
		props = fmt.Sprintf(`<properties><property name="assure.flaky" value="%d"/></properties>`, rerun)
	}
	return `<testcase classname="p" name="` + name + `">` + props + `<failure message="boom">boom</failure></testcase>`
}

func flakySuite(cases ...string) string {
	return fmt.Sprintf(`<testsuite name="p" tests="%d" failures="%d">%s</testsuite>`, len(cases), len(cases), strings.Join(cases, ""))
}

func flakyFixture(t *testing.T, junit string) string {
	t.Helper()
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n")
	writeFile(t, root, ".gitignore", ".assure/state/\n")
	writeFile(t, root, "p/x.xx", "one\n")
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		`describe) printf '%s' '{"protocol":2,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true}}}' ;;` + "\n" +
		`run) mkdir -p "$6"; printf '%s' '` + junit + `' > "$6/junit.xml"
     printf '%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"t":"1"},"pinned_by":{}}' ;;` + "\n" +
		"*) echo \"assure-adapter-xx: unexpected $*\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "init")
	hook("session-start", payload(t, "session-start-startup", root, nil))
	writeFile(t, root, "p/x.xx", "two\n")
	return root
}

func lineNaming(text, name string) string {
	for l := range strings.SplitSeq(text, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

func assertFlakyTold(t *testing.T, text, test string, rerun int) {
	t.Helper()
	if l := lineNaming(text, test); !strings.Contains(l, fmt.Sprintf("rerun %d", rerun)) {
		t.Fatalf("message must name flaky %s with the rerun that passed it (rerun %d), so the human sees it failed then passed:\n%s", test, rerun, text)
	}
}

func assertHumanMustFixOrQuarantine(t *testing.T, code int, out map[string]any) string {
	t.Helper()
	msg, _ := out["systemMessage"].(string)
	if code != 0 || out["decision"] != nil || msg == "" {
		t.Fatalf("got %d %v; a flaky test is not the agent's to fix, so the stop is allowed with a systemMessage, never blocked", code, out)
	}
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "still fail") || !strings.Contains(lower, "quarantine") {
		t.Fatalf("systemMessage must say the flaky tests still fail the check and a human must fix or quarantine them, or the allow reads as a pass:\n%s", msg)
	}
	return msg
}

func TestStopAllowsAFlakyTestAndTellsTheHuman(t *testing.T) {
	root := flakyFixture(t, flakySuite(flakyCase("TestSometimes", 1)))
	code, out := stopWith(t, root, false)
	msg := assertHumanMustFixOrQuarantine(t, code, out)
	assertFlakyTold(t, msg, "p.TestSometimes", 1)
}

func TestStopAllowsOnlyFlakyTestsOnFirstAttemptButTheCheckStillFails(t *testing.T) {
	root := flakyFixture(t, flakySuite(flakyCase("TestSometimes", 1), flakyCase("TestRarely", 3)))
	code, out := stopWith(t, root, false)
	msg := assertHumanMustFixOrQuarantine(t, code, out)
	assertFlakyTold(t, msg, "p.TestSometimes", 1)
	assertFlakyTold(t, msg, "p.TestRarely", 3)
	m, err := app.ManifestFor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !app.FastCheck(m, "HEAD", "cli").Blocking() {
		t.Fatal("`assure check --fast` exits 0 on a tree whose only failures are flaky tests; a flaky test must still fail the check so it cannot merge unnoticed")
	}
}

func TestStopBlocksOnARealFailureAndSetsFlakyTestsApart(t *testing.T) {
	root := flakyFixture(t, flakySuite(flakyCase("TestAlwaysRed", 0), flakyCase("TestSometimes", 2)))
	code, out := stopWith(t, root, false)
	reason, _ := out["reason"].(string)
	if code != 2 || out["decision"] != "block" {
		t.Fatalf("got %d %v; TestAlwaysRed failed every rerun, so it is the agent's to fix and the stop blocks", code, out)
	}
	if l := lineNaming(reason, "p.TestAlwaysRed"); l == "" || strings.Contains(l, "rerun") || strings.Contains(strings.ToLower(l), "flaky") {
		t.Fatalf("block reason must list TestAlwaysRed as a plain failure to fix:\n%s", reason)
	}
	if strings.Contains(lineNaming(reason, "p.TestSometimes"), "p.TestAlwaysRed") {
		t.Fatalf("block reason must list the flaky test apart from the failure to fix:\n%s", reason)
	}
	assertFlakyTold(t, reason, "p.TestSometimes", 2)
	if !strings.Contains(strings.ToLower(reason), "not yours to fix") {
		t.Fatalf("block reason must tell the agent the flaky test is not its to fix in this change, or it will chase a failure it did not cause:\n%s", reason)
	}
}

func flakyPool(t *testing.T) []app.Result {
	t.Helper()
	var pool []app.Result
	for _, junit := range []string{
		flakySuite(flakyCase("TestSometimes", 1)),
		flakySuite(flakyCase("TestSometimes", 1), flakyCase("TestRarely", 3)),
		flakySuite(flakyCase("TestAlwaysRed", 0), flakyCase("TestSometimes", 2)),
	} {
		m, err := app.ManifestFor(flakyFixture(t, junit))
		if err != nil {
			t.Fatal(err)
		}
		for _, res := range app.FastCheck(m, "HEAD", "pool").Results {
			if res.ID == "VER-TESTS-PASS" {
				pool = append(pool, res)
			}
		}
	}
	if len(pool) != 3 {
		t.Fatalf("expected one VER-TESTS-PASS result per flaky fixture, got %+v", pool)
	}
	return pool
}
