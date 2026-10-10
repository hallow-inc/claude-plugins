package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const verifierAdapter = `#!/bin/sh
case "$1" in
describe) printf '%%s' '%s' ;;
run)
  out=""
  while [ $# -gt 0 ]; do [ "$1" = "--out" ] && out="$2"; shift; done
  mkdir -p "$out"
  printf '%%s' '%s' > "$out/junit.xml"
  printf '%%s' '{"protocol":1,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"xx":"1"},"pinned_by":{}}' ;;
*) exit 1 ;;
esac
`

var verifierFiles = []string{"a.xx", "p/b.xx", "p/b_test.xx", "notes.txt"}

func verifierRepo(t *testing.T) (bin, root string) {
	t.Helper()
	bin = t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	isolateGit(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	putFile(t, root, ".gitignore", ".assure/state/\n")
	for _, f := range verifierFiles {
		putFile(t, root, f, "base\n")
	}
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "base")
	return bin, root
}

type verifierCase struct {
	langs      []string
	objectives map[string]map[string]any
	failing    bool
	claimed    bool
}

func drawVerifierCase(t *rapid.T, bin, root string, ids []string) verifierCase {
	var c verifierCase
	c.langs = rapid.SampledFrom([][]string{{"xx"}, {"xx", "yy"}, {"yy"}}).Draw(t, "langs")
	level := rapid.SampledFrom([]string{"A", "B", "C", "D"}).Draw(t, "level")
	putFile(t, root, "assurance.yaml", fmt.Sprintf("version: 0\ncatalog: v0\nlanguages: [%s]\ndefault_level: %s\ncomponents: []\n", strings.Join(c.langs, ", "), level))
	c.objectives = map[string]map[string]any{}
	for _, id := range rapid.SliceOfDistinct(rapid.OneOf(rapid.SampledFrom(ids), rapid.Just("ZZ-UNKNOWN")), rapid.ID).Draw(t, "objectives") {
		c.objectives[id] = map[string]any{"tool": "x", "fast": rapid.Bool().Draw(t, "fast "+id)}
	}
	describe, err := json.Marshal(map[string]any{"protocol": 1, "languages": []string{"xx"}, "claims": []string{"**/*.xx"}, "patterns": map[string]any{"test": []string{"**/*_test.xx"}}, "objectives": c.objectives})
	if err != nil {
		t.Fatalf("%v", err)
	}
	c.failing = rapid.Bool().Draw(t, "failing")
	junit := `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`
	if c.failing {
		junit = `<testsuite name="p" tests="1" failures="1"><testcase classname="p" name="TestBrokenWidget"><failure message="boom">boom</failure></testcase></testsuite>`
	}
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), fmt.Appendf(nil, verifierAdapter, describe, junit), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	for _, f := range rapid.SliceOfDistinct(rapid.SampledFrom(verifierFiles), rapid.ID).Draw(t, "changed") {
		putFile(t, root, f, "changed\n")
		c.claimed = c.claimed || strings.HasSuffix(f, ".xx")
	}
	return c
}

func TestVerifierCheckRunsOnlyVerTestsPass(t *testing.T) {
	gitChecks(t, "25")
	bin, root := verifierRepo(t)
	catalog, err := core.LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	inCatalog := map[string]bool{"ZZ-UNKNOWN": true}
	var ids []string
	for _, o := range catalog.Objectives {
		inCatalog[o.ID] = true
		ids = append(ids, o.ID)
	}

	rapid.Check(t, func(t *rapid.T) {
		gitIn(t, root, "reset", "-q", "--hard", "main")
		gitIn(t, root, "clean", "-fdxq")
		c := drawVerifierCase(t, bin, root, ids)
		m, err := ManifestFor(root)
		if err != nil {
			t.Fatalf("%v", err)
		}
		c.check(t, VerifierCheck(m, "HEAD", "verifier"), inCatalog)
	})
}

func (c verifierCase) check(t *rapid.T, rep Report, inCatalog map[string]bool) {
	out := rep.Render()
	var ran []Result
	for _, res := range rep.Results {
		if inCatalog[res.ID] {
			ran = append(ran, res)
		}
	}
	_, declared := c.objectives[VerifierObjective]
	if !slices.Contains(c.langs, "xx") || !declared || !c.claimed {
		if len(ran) != 0 {
			t.Fatalf("objectives in the verifier check: %+v, want none; adapter declared %v\n%s", ran, c.objectives, out)
		}
		return
	}
	if len(ran) != 1 || ran[0].ID != VerifierObjective {
		t.Fatalf("objectives in the verifier check: %+v, want only %s whatever its fast flag; adapter declared %v\n%s", ran, VerifierObjective, c.objectives, out)
	}
	if c.failing != (ran[0].Status == core.Fail) {
		t.Fatalf("%s status %s with failing=%v\n%s", VerifierObjective, ran[0].Status, c.failing, out)
	}
	if c.failing && (!rep.Blocking() || !strings.Contains(out, "TestBrokenWidget")) {
		t.Fatalf("a failing test must block and be named:\n%s", out)
	}
}
