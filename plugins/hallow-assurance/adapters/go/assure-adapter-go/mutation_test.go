package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func gremlinsStatuses() []string {
	out := make([]string, 0, len(strykerStatus))
	for s := range strykerStatus {
		out = append(out, s)
	}
	return out
}

func noSource(string) (string, error) { return "package x\n", nil }

func TestGremlinsConversionIsTotal(t *testing.T) {
	statuses := gremlinsStatuses()
	rapid.Check(t, func(t *rapid.T) {
		var files []any
		want := map[string]string{}
		for fi := range rapid.IntRange(0, 3).Draw(t, "nfiles") {
			name := fmt.Sprintf("p%d/f.go", fi)
			var muts []any
			for range rapid.IntRange(0, 5).Draw(t, "nmut") {
				m := map[string]any{
					"type":   rapid.SampledFrom([]string{"CONDITIONALS_BOUNDARY", "ARITHMETIC_BASE", "INCREMENT_DECREMENT"}).Draw(t, "type"),
					"status": rapid.SampledFrom(statuses).Draw(t, "status"),
					"line":   rapid.IntRange(1, 300).Draw(t, "line"),
					"column": rapid.IntRange(1, 80).Draw(t, "col"),
				}
				id := fmt.Sprintf("mod/%s:%d:%d:%s", name, m["line"], m["column"], m["type"])
				if _, dup := want[id]; dup {
					continue
				}
				want[id] = strykerStatus[m["status"].(string)]
				muts = append(muts, m)
			}
			files = append(files, map[string]any{"file_name": name, "mutations": muts})
		}
		data, _ := json.Marshal(map[string]any{"go_module": "example.com/m", "files": files})
		rep := newStrykerReport()
		if err := rep.addGremlins(data, "mod", noSource); err != nil {
			t.Fatal(err)
		}
		out, _ := json.Marshal(rep)
		if vs, err := schemas.Validate(schemas.MutationReport, out); err != nil || len(vs) > 0 {
			t.Fatalf("report invalid: %v %v\n%s", err, vs, out)
		}
		got := map[string]string{}
		for p, f := range rep.Files {
			if !strings.HasPrefix(p, "mod/") {
				t.Fatalf("path %q not prefixed with the module directory", p)
			}
			for _, m := range f.Mutants {
				if _, dup := got[m.ID]; dup {
					t.Fatalf("duplicate id %s", m.ID)
				}
				got[m.ID] = m.Status
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("statuses %v, want %v", got, want)
		}
	})
}

func TestGremlinsUnknownStatusIsAnError(t *testing.T) {
	for _, s := range []string{"RUNNABLE", "Lived", ""} {
		data := fmt.Appendf(nil, `{"files":[{"file_name":"a.go","mutations":[{"type":"X","status":%q,"line":1,"column":1}]}]}`, s)
		if err := newStrykerReport().addGremlins(data, "", noSource); err == nil {
			t.Fatalf("status %q accepted; an unmapped status must not become a guess", s)
		}
	}
}

func TestGremlinsRecordedFixtureConverts(t *testing.T) {
	data, err := os.ReadFile("testdata/gremlins/v0.6.0.json")
	if err != nil {
		t.Fatal(err)
	}
	rep := newStrykerReport()
	if err := rep.addGremlins(data, ".", noSource); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(rep)
	ms, err := ev.ParseMutationReport(out)
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, m := range ms {
		if m.Path != "gm.go" {
			t.Fatalf("path %q; module-relative names must become repo-relative", m.Path)
		}
		count[m.Status]++
	}
	if want := map[string]int{"Killed": 3, "Survived": 1, "NoCoverage": 1, "Timeout": 1}; fmt.Sprint(count) != fmt.Sprint(want) {
		t.Fatalf("status counts %v, want %v (recorded Gremlins v0.6.0 run)", count, want)
	}
}

func needGremlins(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gremlins"); err != nil {
		t.Skip("gremlins not on PATH")
	}
}

func mutationReport(t *testing.T, dir string) []ev.Mutant {
	t.Helper()
	out := t.TempDir()
	resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("run failed %d: %s", code, stderr)
	}
	if resp.ToolVersions["gremlins"] == "" {
		t.Fatalf("tool_versions %v lacks gremlins", resp.ToolVersions)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := ev.ParseMutationReport(data)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

const boundarySrc = "package a\n\nfunc Pos(n int) bool {\n\treturn n > 0\n}\n\nfunc Untested(a, b int) int {\n\treturn a + b\n}\n"
const boundaryTest = "package a\n\nimport \"testing\"\n\nfunc TestPos(t *testing.T) {\n\tif !Pos(5) || Pos(-5) {\n\t\tt.Fatal(\"pos\")\n\t}\n}\n"

func TestMutationReportsSurvivorOnChangedLineOnly(t *testing.T) {
	needGremlins(t)
	dir := committedModule(t, map[string]string{"a/a.go": boundarySrc, "a/a_test.go": boundaryTest})
	writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(boundarySrc, "return n > 0", "return n > 0 && n < 1000", 1)})
	ms := mutationReport(t, dir)
	survivedOn4 := false
	for _, m := range ms {
		if m.Path != "a/a.go" {
			t.Fatalf("mutant path %q is not repo-relative", m.Path)
		}
		if m.Line != 4 && m.Status != "Ignored" {
			t.Fatalf("mutant on untouched line %d: %+v; only changed lines may be tested", m.Line, m)
		}
		survivedOn4 = survivedOn4 || m.Status == "Survived"
	}
	if !survivedOn4 {
		t.Fatalf("no survivor on the changed boundary line: %+v", ms)
	}
}

func TestMutationGremlinsMissingIsEnvironment(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": boundarySrc})
	writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(boundarySrc, "return n > 0", "return n > 0 && n < 1000", 1)})
	narrowPath(t, []string{"go", "git"}, nil)
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s; a missing tool is the environment's fault and must reach the evaluator as a failing objective, not an adapter crash", code, stderr)
	}
	environmentIn(t, resp, out, "gremlins", gremlinsDefaultInstall)
}

func TestMutationNoGoChangesGivesEmptyReport(t *testing.T) {
	needGremlins(t)
	dir := committedModule(t, map[string]string{"a/a.go": boundarySrc, "README.md": "x\n"})
	writeFiles(t, dir, map[string]string{"README.md": "y\n"})
	if ms := mutationReport(t, dir); len(ms) != 0 {
		t.Fatalf("got %+v, want no mutants", ms)
	}
}

func TestRapidSeedDependsOnlyOnHead(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	a, err := rapidSeed(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"a/b.go": "package a\n"})
	b, _ := rapidSeed(dir)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "two")
	c, _ := rapidSeed(dir)
	if a != b || a == c || a == "0" {
		t.Fatalf("seeds %s %s %s; the seed must be fixed per commit, differ across commits, and never be 0 (random)", a, b, c)
	}
}

func TestMutationModuleBelowGitRoot(t *testing.T) {
	needGremlins(t)
	top := t.TempDir()
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	writeFiles(t, top, map[string]string{"mod/go.mod": "module example.com/m\n\ngo 1.26\n", "mod/a/a.go": boundarySrc, "mod/a/a_test.go": boundaryTest})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-qm", "init")
	dir := filepath.Join(top, "mod")
	writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(boundarySrc, "return n > 0", "return n > 0 && n < 1000", 1)})
	tested := 0
	for _, m := range mutationReport(t, dir) {
		if m.Status != "Ignored" {
			tested++
		}
	}
	if tested == 0 {
		t.Fatal("every mutant Ignored; Gremlins matched git-root diff paths against module-relative names, so nothing was tested")
	}
}
