package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var evidencePaths = []string{"low/a.go", "src/b.go", "x/c.go", "x/d.go"}

func evidenceManifest(t fataler, dir string) *core.Manifest {
	t.Helper()
	file := filepath.Join(dir, "assurance.yaml")
	if err := os.WriteFile(file, []byte("version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents:\n  - {path: 'low/**', level: C}\n"), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	m, err := core.LoadManifest(file)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return m
}

func TestReadEvidenceFuzzKeepsSourcesAndEveryCaseOutcome(t *testing.T) {
	m := evidenceManifest(t, t.TempDir())
	tags := map[string]string{"passed": "", "failed": "<failure message=\"m\"/>", "errored": "<error message=\"m\"/>", "skipped": "<skipped/>"}
	rapid.Check(t, func(t *rapid.T) {
		var doc strings.Builder
		doc.WriteString("<testsuites>")
		var want core.FuzzRun
		for i := range rapid.IntRange(0, 3).Draw(t, "suites") {
			var s core.FuzzSuite
			fmt.Fprintf(&doc, `<testsuite name="s%d"><properties><property name="other" value="ignored"/>`, i)
			for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(evidencePaths), 0, 3, rapid.ID[string]).Draw(t, "sources") {
				fmt.Fprintf(&doc, `<property name="assure.source" value="%s"/>`, p)
				s.Sources = append(s.Sources, p)
			}
			doc.WriteString("</properties>")
			for j := range rapid.IntRange(0, 3).Draw(t, "cases") {
				outcome := rapid.SampledFrom([]string{"passed", "failed", "errored", "skipped"}).Draw(t, "outcome")
				name := fmt.Sprintf("Fuzz%d_%d", i, j)
				fmt.Fprintf(&doc, `<testcase name="%s">%s</testcase>`, name, tags[outcome])
				s.Cases = append(s.Cases, core.FuzzCase{Name: name, Outcome: outcome})
			}
			doc.WriteString("</testsuite>")
			want.Suites = append(want.Suites, s)
		}
		doc.WriteString("</testsuites>")
		var ev core.Evidence
		readEvidence(m, "fuzz.run", []byte(doc.String()), &ev)
		if len(ev.Problems) > 0 || ev.Fuzz == nil {
			t.Fatalf("problems %v, fuzz %v for %s", ev.Problems, ev.Fuzz, doc.String())
		}
		if !reflect.DeepEqual(*ev.Fuzz, want) {
			t.Fatalf("got %+v, want %+v for %s", *ev.Fuzz, want, doc.String())
		}
	})
}

func TestReadEvidenceBudgetTakesLevelFromTheManifest(t *testing.T) {
	m := evidenceManifest(t, t.TempDir())
	rapid.Check(t, func(t *rapid.T) {
		files := []map[string]any{}
		var want core.TestBudget
		for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(evidencePaths), 0, 4, rapid.ID[string]).Draw(t, "paths") {
			n := rapid.IntRange(0, 50).Draw(t, "count")
			_, level := m.Resolve(p)
			if rapid.Bool().Draw(t, "is test") {
				files = append(files, map[string]any{"path": p, "role": "test", "added_cases": n})
				want.Files = append(want.Files, core.BudgetEntry{Path: p, Level: level, Test: true, Cases: n})
			} else {
				files = append(files, map[string]any{"path": p, "role": "source", "changed_lines": n})
				want.Files = append(want.Files, core.BudgetEntry{Path: p, Level: level, Lines: n})
			}
		}
		data, err := json.Marshal(map[string]any{"version": 0, "files": files})
		if err != nil {
			t.Fatal(err)
		}
		var ev core.Evidence
		readEvidence(m, "test.budget", data, &ev)
		if len(ev.Problems) > 0 || ev.Budget == nil {
			t.Fatalf("problems %v, budget %v for %s", ev.Problems, ev.Budget, data)
		}
		if !reflect.DeepEqual(*ev.Budget, want) {
			t.Fatalf("got %+v, want %+v", *ev.Budget, want)
		}
	})
}

func TestReadEvidenceCoverageCarriesLinesFunctionsAndResolutions(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp("", "assure-evidence")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		m := evidenceManifest(t, dir)
		var lcov strings.Builder
		var want core.Coverage
		for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(evidencePaths), 0, 3, rapid.ID[string]).Draw(t, "files") {
			cf := core.CoverFile{Path: p, Lines: map[int]int{}}
			fmt.Fprintf(&lcov, "TN:\nSF:%s\n", p)
			for i := range rapid.IntRange(0, 3).Draw(t, "funcs") {
				fn := core.CoverFunc{Name: fmt.Sprintf("F%d", i), Start: 10*i + 1, End: 10*i + 9}
				fmt.Fprintf(&lcov, "FN:%d,%d,%s\nFNDA:1,%s\n", fn.Start, fn.End, fn.Name, fn.Name)
				cf.Funcs = append(cf.Funcs, fn)
			}
			for _, l := range rapid.SliceOfNDistinct(rapid.IntRange(1, 40), 0, 6, rapid.ID[int]).Draw(t, "lines") {
				hits := rapid.IntRange(0, 3).Draw(t, "hits")
				fmt.Fprintf(&lcov, "DA:%d,%d\n", l, hits)
				cf.Lines[l] = hits
			}
			lcov.WriteString("end_of_record\n")
			want.Files = append(want.Files, cf)
		}
		var yaml []map[string]string
		for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(evidencePaths), 0, 3, rapid.ID[string]).Draw(t, "resolved") {
			r := core.Resolution{Path: p, Function: "F0", Resolution: "dead", Rationale: "unreachable since the v2 migration", Approver: "@owner"}
			yaml = append(yaml, map[string]string{"path": r.Path, "function": r.Function, "resolution": r.Resolution, "rationale": r.Rationale, "approver": r.Approver})
			want.Resolutions = append(want.Resolutions, r)
		}
		if yaml != nil {
			data, err := json.Marshal(yaml)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(m.Root, ".assure"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(m.Root, core.ResolutionsFile), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var ev core.Evidence
		readEvidence(m, "coverage.resolution", []byte(lcov.String()), &ev)
		if len(ev.Problems) > 0 || ev.Coverage == nil {
			t.Fatalf("problems %v, coverage %v for %s", ev.Problems, ev.Coverage, lcov.String())
		}
		if !reflect.DeepEqual(*ev.Coverage, want) {
			t.Fatalf("got %+v, want %+v", *ev.Coverage, want)
		}
	})
}

func TestReadEvidenceMalformedInputIsAProblemNotEvidence(t *testing.T) {
	m := evidenceManifest(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(m.Root, ".assure"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, core.ResolutionsFile), []byte(`[{"path": "a.go"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"fuzz.run":            "<nope/>",
		"test.budget":         `{"version": 0, "files": [{"path": "a.go", "role": "test"}]}`,
		"coverage.resolution": "SF:a.go\nDA:1,1\nend_of_record\n",
	}
	for typ, data := range cases {
		var ev core.Evidence
		readEvidence(m, typ, []byte(data), &ev)
		if len(ev.Problems) == 0 || ev.Fuzz != nil || ev.Budget != nil || ev.Coverage != nil {
			t.Errorf("%s: problems %v, fuzz %v, budget %v, coverage %v; unreadable evidence must fail closed, not pass as empty", typ, ev.Problems, ev.Fuzz, ev.Budget, ev.Coverage)
		}
	}
}
