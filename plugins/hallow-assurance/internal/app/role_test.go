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

func drawInspection(t *rapid.T) ([]byte, map[string]int) {
	counts := map[string]int{}
	results := []map[string]any{}
	for range rapid.IntRange(0, 5).Draw(t, "results") {
		level := rapid.SampledFrom(InspectionLevels).Draw(t, "level")
		counts[level]++
		results = append(results, map[string]any{
			"ruleId":  rapid.StringMatching("[A-Za-z0-9`/ -]{1,12}").Draw(t, "ruleId"),
			"level":   level,
			"message": map[string]any{"text": rapid.StringN(1, 30, -1).Draw(t, "text")},
			"locations": []any{map[string]any{"physicalLocation": map[string]any{
				"artifactLocation": map[string]any{"uri": rapid.StringMatching(`[a-z]{1,5}(/[a-z_]{1,5}){0,2}\.go`).Draw(t, "uri")},
				"region":           map[string]any{"startLine": rapid.IntRange(1, 1<<20).Draw(t, "line")},
			}}},
		})
	}
	log := map[string]any{
		"version": "2.1.0",
		"runs":    []any{map[string]any{"tool": map[string]any{"driver": map[string]any{"name": "hallow-assurance:inspector"}}, "results": results}},
	}
	var data []byte
	var err error
	if indent := rapid.SampledFrom([]string{"", " ", "  ", "\t", "-"}).Draw(t, "indent"); indent == "-" {
		data, err = json.Marshal(log)
	} else {
		data, err = json.MarshalIndent(log, "", indent)
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	pad := rapid.StringMatching("[ \t\n]{0,3}")
	return slices.Concat([]byte(pad.Draw(t, "lead")), data, []byte(pad.Draw(t, "trail"))), counts
}

func isSarifFence(line string) bool { return strings.TrimSuffix(line, "\r") == "```sarif" }

func drawProse(t *rapid.T, label string, forbid func(string) bool) []string {
	line := rapid.OneOf(
		rapid.SampledFrom([]string{"```", "```json", "```go", "```sarif ", " ```sarif", "````sarif", "```SARIF", "sarif", "", "``` "}),
		rapid.StringMatching(`[^\r\n]{0,24}`),
	).Filter(func(s string) bool { return !forbid(s) })
	return rapid.SliceOfN(line, 0, 5).Draw(t, label)
}

func TestExtractSARIFReturnsTheOnlyBlockUnchanged(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		log, counts := drawInspection(t)
		sep := rapid.SampledFrom([]string{"\n", "\r\n"}).Draw(t, "sep")
		lines := slices.Concat(drawProse(t, "before", isSarifFence), []string{"```sarif", string(log), "```"}, drawProse(t, "after", isSarifFence))
		text := strings.Join(lines, sep)
		if rapid.Bool().Draw(t, "trailing") {
			text += sep
		}
		got, err := ExtractSARIF(text)
		if err != nil || string(got) != string(log) {
			t.Fatalf("ExtractSARIF = %q, %v; want the embedded log %q byte for byte\ntext: %q", got, err, log, text)
		}
		data, gotCounts, err := Inspect(text)
		if err != nil || string(data) != string(log) {
			t.Fatalf("Inspect = %q, %v; a valid inspection log in the one sarif block must pass", data, err)
		}
		if want := fmt.Sprintf("%d error, %d warning, %d note", counts["error"], counts["warning"], counts["note"]); RenderCounts(gotCounts) != want {
			t.Fatalf("RenderCounts = %q, want %q", RenderCounts(gotCounts), want)
		}
	})
}

func TestExtractSARIFRejectsAnyBlockCountButOne(t *testing.T) {
	isClose := func(s string) bool { return strings.TrimSuffix(s, "\r") == "```" }
	rapid.Check(t, func(t *rapid.T) {
		blocks := rapid.SampledFrom([]int{0, 0, 2, 3, 4}).Draw(t, "blocks")
		unclosed := rapid.Bool().Draw(t, "unclosed")
		sep := rapid.SampledFrom([]string{"\n", "\r\n"}).Draw(t, "sep")
		lines := drawProse(t, "before", isSarifFence)
		if rapid.Bool().Draw(t, "jsonFence") {
			log, _ := drawInspection(t)
			lines = append(lines, "```json", string(log), "```")
		}
		for i := range blocks {
			lines = append(lines, "```sarif")
			lines = append(lines, drawProse(t, fmt.Sprintf("body%d", i), isClose)...)
			lines = append(lines, "```")
			lines = append(lines, drawProse(t, fmt.Sprintf("between%d", i), isSarifFence)...)
		}
		openIdx := len(lines)
		if unclosed {
			lines = append(lines, "```sarif")
			lines = append(lines, drawProse(t, "tail", isClose)...)
		}
		text := strings.Join(lines, sep)
		openPos := len(strings.Join(lines[:openIdx], sep))
		if openIdx > 0 {
			openPos += len(sep)
		}
		got, err := ExtractSARIF(text)
		if err == nil || got != nil {
			t.Fatalf("ExtractSARIF(%q) = %q, %v; %d complete blocks (unclosed=%v) must be rejected", text, got, err, blocks, unclosed)
		}
		var want string
		switch {
		case unclosed:
			want = fmt.Sprintf("```sarif block opened on line %d ", strings.Count(text[:openPos], "\n")+1)
		case blocks == 0:
			want = "no ```sarif block was found"
		default:
			want = fmt.Sprintf("%d ```sarif blocks were found", blocks)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("reason %q does not contain %q; the reason must name the block count", err, want)
		}
		if _, _, ierr := Inspect(text); ierr == nil {
			t.Fatalf("Inspect accepted %q", text)
		}
	})
}

func FuzzExtractSARIF(f *testing.F) {
	f.Add("")
	f.Add("```sarif\n{}\n```")
	f.Add("intro\r\n```sarif\r\n{\"runs\":[]}\r\n```\r\noutro\r\n")
	f.Add("```json\n{}\n```\n")
	f.Add("```sarif\na\n```\n```sarif\nb\n```\n")
	f.Add("```sarif\n```sarif\n{}\n")
	f.Add("```sarif\n```")
	f.Fuzz(func(t *testing.T, text string) {
		got, err := ExtractSARIF(text)
		if err != nil {
			if got != nil || !strings.Contains(err.Error(), "```sarif") {
				t.Fatalf("error %q with block %q; a rejection returns no block and names the fence", err, got)
			}
			return
		}
		if !strings.Contains(text, "```sarif\n"+string(got)) && !strings.Contains(text, "```sarif\r\n"+string(got)) {
			t.Fatalf("block %q does not follow a ```sarif line in %q", got, text)
		}
	})
}

const verifierAdapter = `#!/bin/sh
case "$1" in
describe) printf '%%s' '%s' ;;
run)
  out=""
  while [ $# -gt 0 ]; do [ "$1" = "--out" ] && out="$2"; shift; done
  mkdir -p "$out"
  printf '%%s' '%s' > "$out/junit.xml"
  printf '%%s' '{"protocol":0,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"xx":"1"}}' ;;
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
	describe, err := json.Marshal(map[string]any{"protocol": 0, "languages": []string{"xx"}, "claims": []string{"**/*.xx"}, "patterns": map[string]any{"test": []string{"**/*_test.xx"}}, "objectives": c.objectives})
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
