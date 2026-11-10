package app_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const flakyJUnitSrc = "fixture-junit.xml"

func flakyRepo(t *testing.T) (*core.Manifest, string) {
	t.Helper()
	root := fastRefRepo(t)
	fastRefPut(t, root, ".gitignore", ".assure/state/\n/"+flakyJUnitSrc+"\n")
	fastRefPut(t, root, "p/x.xx", "one\n")
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":2,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true}}}' ;;
run) mkdir -p "$6"; cp ` + flakyJUnitSrc + ` "$6/junit.xml"
     printf '%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"t":"1"},"pinned_by":{}}' ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	fastRefGit(t, root, "init", "-q")
	fastRefGit(t, root, "add", "-A")
	fastRefGit(t, root, "commit", "-qm", "init")
	fastRefPut(t, root, "p/x.xx", "two\n")
	m, err := app.ManifestFor(root)
	if err != nil {
		t.Fatal(err)
	}
	return m, root
}

type flakyCase struct {
	name  string
	rerun int
	fails bool
}

func flakyJUnit(cases []flakyCase) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<testsuite name="p" tests="%d">`, len(cases))
	for _, c := range cases {
		fmt.Fprintf(&b, `<testcase classname="p" name="%s">`, c.name)
		if c.rerun > 0 {
			fmt.Fprintf(&b, `<properties><property name="assure.flaky" value="%d"/></properties>`, c.rerun)
		}
		if c.fails || c.rerun > 0 {
			b.WriteString(`<failure message="boom">boom</failure>`)
		}
		b.WriteString(`</testcase>`)
	}
	b.WriteString(`</testsuite>`)
	return b.String()
}

func flakyFastCheck(t *rapid.T, m *core.Manifest, root string, cases []flakyCase) (app.Report, app.Result) {
	if err := os.WriteFile(filepath.Join(root, flakyJUnitSrc), []byte(flakyJUnit(cases)), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	rep := app.FastCheck(m, "HEAD", "flaky")
	for _, res := range rep.Results {
		if res.ID == "VER-TESTS-PASS" {
			return rep, res
		}
	}
	t.Fatalf("no VER-TESTS-PASS result in %+v", rep.Results)
	return rep, app.Result{}
}

func lineNaming(out, name string) string {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

func TestFlakyTestsFailTheCheckButAreNotTheAgentsToFix(t *testing.T) {
	m, root := flakyRepo(t)
	rapid.Check(t, func(t *rapid.T) {
		var cases, settled []flakyCase
		var hard, flaky []flakyCase
		for i := range rapid.IntRange(1, 4).Draw(t, "tests") {
			c := flakyCase{name: fmt.Sprintf("Test%c", 'A'+i)}
			switch rapid.SampledFrom([]string{"pass", "fail", "flaky"}).Draw(t, "outcome") {
			case "fail":
				c.fails = true
				hard = append(hard, c)
			case "flaky":
				c.rerun = rapid.IntRange(1, 3).Draw(t, "rerun")
				flaky = append(flaky, c)
			}
			cases = append(cases, c)
			settled = append(settled, flakyCase{name: c.name, fails: c.fails})
		}
		rep, res := flakyFastCheck(t, m, root, cases)
		failed := len(hard)+len(flaky) > 0
		if (res.Status == core.Fail) != failed || rep.Blocking() != failed {
			t.Fatalf("status %s, blocking %v with %d failing and %d flaky tests: a flaky test still fails the check, so `assure check` must exit non-zero", res.Status, rep.Blocking(), len(hard), len(flaky))
		}
		calm := rep
		if len(flaky) > 0 {
			calm, _ = flakyFastCheck(t, m, root, settled)
		}
		if core.StopFingerprint(rep.Failures()) != core.StopFingerprint(calm.Failures()) {
			t.Fatalf("Failures() = %+v differs in fingerprint from the same run with its flaky tests passing (%+v); a flaky test must not open a new block budget, and an objective failing only on flaky tests must not count", rep.Failures(), calm.Failures())
		}
		if len(flaky) > 0 && rep.OutsideReach() != (len(hard) == 0) {
			t.Fatalf("OutsideReach() = %v with %d failing and %d flaky tests: only flaky failures are the human's to fix or quarantine, any other failure is the agent's", rep.OutsideReach(), len(hard), len(flaky))
		}
		assertRenderSetsFlakyApart(t, rep.Render(), hard, flaky)
	})
}

func assertRenderSetsFlakyApart(t *rapid.T, out string, hard, flaky []flakyCase) {
	if len(flaky) > 0 && !strings.Contains(strings.ToLower(out), "flaky") {
		t.Fatalf("Render() does not call the flaky tests flaky:\n%s", out)
	}
	for _, c := range flaky {
		if l := lineNaming(out, c.name); !strings.Contains(l, fmt.Sprintf("rerun %d", c.rerun)) {
			t.Fatalf("Render() names %s without the rerun that passed it (rerun %d), so a human cannot tell it is flaky:\n%s", c.name, c.rerun, out)
		}
	}
	for _, c := range hard {
		l := lineNaming(out, c.name)
		if l == "" || strings.Contains(l, "rerun") {
			t.Fatalf("Render() must list %s, which failed every rerun, as a plain failure:\n%s", c.name, out)
		}
	}
}
