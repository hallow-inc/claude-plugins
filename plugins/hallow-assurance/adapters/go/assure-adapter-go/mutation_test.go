package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
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

func unleashArgv(t *testing.T, ref, target string) []string {
	t.Helper()
	argv := filepath.Join(t.TempDir(), "argv")
	narrowPath(t, nil, map[string]string{"gremlins": `printf '%s\n' "$@" >| '` + argv + `'
while [ $# -gt 0 ]; do
	if [ "$1" = --output ]; then shift; printf '{"files":[]}' >| "$1"; fi
	shift
done
`})
	if _, err := unleash(t.TempDir(), ref, target, t.TempDir(), "1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func flagValue(args []string, name string) string {
	v := ""
	for i, a := range args {
		if a == name && i+1 < len(args) {
			v = args[i+1]
		} else if s, ok := strings.CutPrefix(a, name+"="); ok {
			v = s
		}
	}
	return v
}

func TestMutationGivesGremlinsTimeoutHeadroom(t *testing.T) {
	const minCoefficient = 100
	args := unleashArgv(t, "HEAD", ".")
	coef := flagValue(args, "--timeout-coefficient")
	if n, err := strconv.Atoi(coef); err != nil || n < minCoefficient {
		t.Fatalf("gremlins argv %q: --timeout-coefficient %q, want an integer >= %d; a mutant whose test run outlasts the coefficient is reported TIMED OUT, and TIMED OUT counts as detected, so a tight coefficient turns real survivors into false kills and VER-MUTATION-CHANGED fails open on packages with slow tests (measured on internal/hookio at --workers 2: coefficient 10 gave 15 timed out / 0 lived, coefficient 100 gave 68 killed / 9 lived / 0 timed out)", args, coef, minCoefficient)
	}
}

func TestMutationBoundsGremlinsWorkers(t *testing.T) {
	const maxWorkers = 2
	argv := filepath.Join(t.TempDir(), "argv")
	narrowPath(t, nil, map[string]string{"gremlins": `printf '%s\n' "$@" >| '` + argv + `'
while [ $# -gt 0 ]; do
	if [ "$1" = --output ]; then shift; printf '{"files":[]}' >| "$1"; fi
	shift
done
`})
	if _, err := unleash(t.TempDir(), "HEAD", ".", t.TempDir(), "1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	workers := ""
	for i, a := range args {
		if a == "--workers" && i+1 < len(args) {
			workers = args[i+1]
		} else if v, ok := strings.CutPrefix(a, "--workers="); ok {
			workers = v
		}
	}
	if n, err := strconv.Atoi(workers); err != nil || n < 1 || n > maxWorkers {
		t.Fatalf("gremlins argv %q: --workers %q, want 1..%d; Gremlins defaults to one worker per CPU, which starves each mutant's test run past the timeout coefficient, and TIMED OUT counts as detected, so surviving mutants are scored as kills (measured on internal/decode: 33 false timeouts, 3 survivors hidden)", args, workers, maxWorkers)
	}
}

func TestMutationNoGoChangesGivesEmptyReport(t *testing.T) {
	needGremlins(t)
	dir := committedModule(t, map[string]string{"a/a.go": boundarySrc, "README.md": "x\n"})
	writeFiles(t, dir, map[string]string{"README.md": "y\n"})
	if ms := mutationReport(t, dir); len(ms) != 0 {
		t.Fatalf("got %+v, want no mutants", ms)
	}
}

const declsOnlySrc = "package a\n\ntype Point struct {\n\tX, Y int\n}\n\nconst Origin = \"origin\"\n\nfunc Name() string {\n\treturn Origin\n}\n"

func TestMutationChangedPackageWithNothingToMutateGivesEmptyReport(t *testing.T) {
	changed := func(t *testing.T) string {
		t.Helper()
		dir := committedModule(t, map[string]string{"a/a.go": declsOnlySrc})
		writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(declsOnlySrc, "X, Y int", "X, Y, Z int", 1)})
		return dir
	}
	t.Run("real gremlins, nothing to mutate, gives empty report", func(t *testing.T) {
		needGremlins(t)
		dir := changed(t)
		if got := selection(t, dir); len(got) == 0 {
			t.Fatalf("selection %v is empty; the fixture must select the changed package or it does not exercise Gremlins", got)
		}
		out := t.TempDir()
		resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--changed-from", "HEAD", "--out", out)
		if code != 0 {
			t.Fatalf("run failed %d: %s; a changed package with no mutable operator has zero mutants, which is an empty report for the evaluator to judge, not a tool error that fails the run", code, stderr)
		}
		data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
		if err != nil {
			t.Fatal(err)
		}
		ms, err := ev.ParseMutationReport(data)
		if err != nil {
			t.Fatalf("mutation report invalid: %v\n%s", err, data)
		}
		if len(ms) != 0 {
			t.Fatalf("got %+v, want no mutants; the changed package has no arithmetic, relational, or logical operator", ms)
		}
	})
	t.Run("silent empty output without marker fails closed", func(t *testing.T) {
		dir := changed(t)
		narrowPath(t, []string{"git"}, withStubs(gremlinsStubs(t, "0.6.0"), map[string]string{"gremlins": "echo 'Mutation testing completed.'\nexit 0\n"}))
		out := filepath.Join(t.TempDir(), "ev")
		resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--changed-from", "HEAD", "--out", out)
		if code == 0 {
			t.Fatalf("run exited 0 with response %+v (stderr %q) after Gremlins exited 0, wrote no --output, and never printed \"No results to report.\"; silent empty output without that marker is missing evidence, and missing evidence must fail closed, not pass as an empty mutation report", resp, stderr)
		}
		if !strings.Contains(stderr, "gremlins output") {
			t.Fatalf("run exited %d with stderr %q; the failure must name the empty Gremlins output, not an unrelated fault in the fixture", code, stderr)
		}
	})
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

const toolBase = "package main\n\nfunc main() {}\n\nfunc Pos(n int) bool { return false }\nfunc Add(a, b int) int { return 0 }\n"
const toolTest = "package main\n\nimport \"testing\"\n\nfunc TestPos(t *testing.T) {\n\tif Pos(0) || !Pos(1) {\n\t\tt.Fatal(\"pos\")\n\t}\n}\n\nfunc TestAddRuns(t *testing.T) {\n\t_ = Add(1, 2)\n}\n"

func TestMutationPackageMainJudgedByItsOwnTests(t *testing.T) {
	needGremlins(t)
	const posLine, addLine = 5, 6
	for _, root := range []struct{ name, test, bug string }{
		{"root test fails in gremlins copy", "package m\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestRootReadsOutsideModule(t *testing.T) {\n\tif _, err := os.Stat(\"../outside\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n",
			"the root package's test reads a file outside the module, so it passes in the real tree and fails in Gremlins' copy of the module; a main-package mutant judged by it is KILLED whatever its own tests do"},
		{"root test always passes", "package m\n\nimport \"testing\"\n\nfunc TestRoot(t *testing.T) {}\n",
			"the root package's test passes and never calls cmd/tool, so a main-package mutant judged by it LIVES whatever its own tests do"},
	} {
		for _, selector := range []string{"--changed-from", "--scope"} {
			t.Run(root.name+" "+selector, func(t *testing.T) {
				top := t.TempDir()
				if real, err := filepath.EvalSymlinks(top); err == nil {
					top = real
				}
				writeFiles(t, top, map[string]string{"outside": "x\n", "mod/go.mod": "module example.com/m\n\ngo 1.26\n",
					"mod/root.go": "package m\n", "mod/root_test.go": root.test,
					"mod/cmd/tool/main.go": toolBase, "mod/cmd/tool/main_test.go": toolTest})
				git(t, top, "init", "-q")
				git(t, top, "add", "-A")
				git(t, top, "commit", "-qm", "init")
				dir := filepath.Join(top, "mod")
				writeFiles(t, dir, map[string]string{"cmd/tool/main.go": strings.NewReplacer("return false", "return n > 0", "return 0", "return a + b").Replace(toolBase)})
				checkMainMutants(t, mainMutationReport(t, dir, selector, posLine, addLine), "cmd/tool/main.go", root.bug, selector, posLine, addLine)
			})
		}
	}
}

func mainMutationReport(t *testing.T, dir, selector string, posLine, addLine int) []ev.Mutant {
	t.Helper()
	if selector == "--changed-from" {
		return mutationReport(t, dir)
	}
	scope := writeScope(t, adapterkit.Scope{{Path: "cmd/tool/main.go", Ranges: [][2]int{{posLine, addLine}}}})
	out := t.TempDir()
	resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--scope", scope, "--out", out)
	if code != 0 || len(resp.Evidence) != 1 {
		t.Fatalf("exit %d %s, response %+v", code, stderr, resp)
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

func checkMainMutants(t *testing.T, ms []ev.Mutant, file, bug, selector string, posLine, addLine int) {
	t.Helper()
	want := map[int]string{posLine: "Killed", addLine: "Survived"}
	seen := map[int]int{}
	for _, m := range ms {
		if m.Status == "Ignored" {
			continue
		}
		if m.Path != file || want[m.Line] == "" {
			t.Fatalf("%s run: mutant %+v outside lines %d and %d of %s", selector, m, posLine, addLine, file)
		}
		seen[m.Line]++
		if m.Status != want[m.Line] {
			t.Fatalf("%s run: mutant %+v is %s, want %s (all mutants: %+v); %s. A package main file's mutation score must reflect its own tests (TestPos catches every mutant on line %d, nothing asserts Add on line %d), otherwise VER-MUTATION-CHANGED passes or fails for reasons unrelated to the change",
				selector, m, m.Status, want[m.Line], ms, bug, posLine, addLine)
		}
	}
	if seen[posLine] == 0 || seen[addLine] == 0 {
		t.Fatalf("%s run: tested mutants per line %v, want at least one on lines %d and %d: %+v", selector, seen, posLine, addLine, ms)
	}
}

const generatorSrc = "//go:build ignore\n\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(1 + 2)\n}\n"

func TestMutationSkipsBuildIgnoredGeneratorDir(t *testing.T) {
	needGremlins(t)
	for _, selector := range []string{"--changed-from", "--scope"} {
		t.Run(selector, func(t *testing.T) {
			dir := committedModule(t, map[string]string{"a/a.go": boundarySrc, "a/a_test.go": boundaryTest, "gen/gen.go": generatorSrc})
			writeFiles(t, dir, map[string]string{
				"a/a.go":     strings.Replace(boundarySrc, "return n > 0", "return n > 0 && n < 1000", 1),
				"gen/gen.go": strings.Replace(generatorSrc, "1 + 2", "1 + 3", 1),
			})
			out := t.TempDir()
			args := []string{"run", "VER-MUTATION-CHANGED", "--changed-from", "HEAD", "--out", out}
			if selector == "--scope" {
				args = []string{"run", "VER-MUTATION-CHANGED", "--scope", writeScope(t, adapterkit.Scope{{Path: "go.mod"}, {Path: "a/a.go"}}), "--out", out}
			}
			resp, stderr, code := runIn(t, dir, args...)
			if code != 0 || len(resp.Evidence) != 1 {
				t.Fatalf("%s run exited %d, response %+v:\n%s\ngen/gen.go is a generator behind `//go:build ignore`, a common layout; its directory holds no buildable package, Gremlins v0.6.0 exits 1 there (\"no packages to test\"), and a run on it must not fail VER-MUTATION-CHANGED for the whole module", selector, code, resp, stderr)
			}
			data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
			if err != nil {
				t.Fatal(err)
			}
			ms, err := ev.ParseMutationReport(data)
			if err != nil {
				t.Fatal(err)
			}
			tested := 0
			for _, m := range ms {
				if m.Path != "a/a.go" {
					t.Fatalf("%s run reported mutant %+v; gen/gen.go is excluded by `//go:build ignore` from every build, so no test can kill its mutants and none may count against the score", selector, m)
				}
				if m.Status != "Ignored" {
					tested++
				}
			}
			if tested == 0 {
				t.Fatalf("%s run tested no mutant of a/a.go: %+v; skipping the build-ignored generator directory must not also skip the real package beside it", selector, ms)
			}
		})
	}
}

func TestScopedNestedPackageMainJudgedByItsOwnTests(t *testing.T) {
	needGremlins(t)
	const posLine, addLine = 5, 6
	tool := strings.NewReplacer("return false", "return n > 0", "return 0", "return a + b").Replace(toolBase)
	dir := committedModule(t, map[string]string{
		"a/a.go": "package a\n", "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"a/cmd/tool/main.go": tool, "a/cmd/tool/main_test.go": toolTest,
	})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go"}, {Path: "a/cmd/tool/main.go"}})
	out := t.TempDir()
	resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--scope", scope, "--out", out)
	if code != 0 || len(resp.Evidence) != 1 {
		t.Fatalf("exit %d %s, response %+v", code, stderr, resp)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	var rep strykerReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, f := range rep.Files {
		for _, m := range f.Mutants {
			seen[m.ID]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("mutant %s reported %d times in %v; a run on a must not also report a/cmd/tool's mutants when a/cmd/tool has its own run, or a duplicated survivor counts twice against the mutation score", id, n, seen)
		}
	}
	ms, err := ev.ParseMutationReport(data)
	if err != nil {
		t.Fatal(err)
	}
	checkMainMutants(t, ms, "a/cmd/tool/main.go", "a's tests pass and never call a/cmd/tool, so a main-package mutant judged by them LIVES whatever its own tests do; a scoped package nested under another scoped package must get its own run", "--scope", posLine, addLine)
}

const twoChangeSrc = "package a\n\nfunc Pos(n int) bool {\n\treturn n > 0\n}\n\nfunc Neg(n int) bool {\n\treturn n < 0\n}\n"
const twoChangeTest = "package a\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif !Pos(5) || Pos(-5) || !Neg(-5) || Neg(5) {\n\t\tt.Fatal(\"sign\")\n\t}\n}\n"

func TestMutationTestsEveryChangeInOneDiffHunk(t *testing.T) {
	needGremlins(t)
	dir := committedModule(t, map[string]string{"a/a.go": twoChangeSrc, "a/a_test.go": twoChangeTest})
	head := strings.Replace(twoChangeSrc, "return n > 0", "return n > 0 && n < 1000", 1)
	head = strings.Replace(head, "return n < 0", "return n < 0 && n > -1000", 1)
	writeFiles(t, dir, map[string]string{"a/a.go": head})
	ms := mutationReport(t, dir)
	testedOn8 := false
	for _, m := range ms {
		if m.Line != 8 {
			continue
		}
		if m.Status == "Ignored" {
			t.Fatalf("mutant on changed line 8 reported Ignored: %+v; lines 4 and 8 share one 3-line-context diff hunk, and a change the mutation run skips hides its survivors, so VER-MUTATION-CHANGED passes on untested code", m)
		}
		testedOn8 = true
	}
	if !testedOn8 {
		t.Fatalf("no mutant on changed line 8: %+v", ms)
	}
}

const nonViableWhy = "Gremlins v0.6.0 maps `go test` exit 1 to KILLED and exit 2 to NOT VIABLE, and `go test` exits 1 when the mutant does not compile; a non-compiling mutant counted as KILLED inflates the mutation score and can lift VER-MUTATION-CHANGED over its threshold (go-gremlins/gremlins#321)"

func wrappedGo(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	env, cleanup, err := goWrapperEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if env == nil {
		t.Fatal("goWrapperEnv installed no wrapper with go on PATH")
	}
	cmd := exec.CommandContext(t.Context(), "sh", append([]string{"-c", `exec go "$@"`, "sh"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
	return exit.ExitCode(), string(out)
}

func TestGoWrapperReportsUnbuildableTestAsNotViable(t *testing.T) {
	const passTest = "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = F() }\n"
	for _, c := range []struct {
		name  string
		files map[string]string
		want  int
		why   string
	}{
		{"mutated source fails to type-check",
			map[string]string{"a/a.go": "package a\n\nfunc F() [2]int { return [1 - 1]int{} }\n", "a/a_test.go": passTest},
			2, "`FAIL <pkg> [build failed]`: " + nonViableWhy},
		{"test file references a symbol the mutant broke",
			map[string]string{"a/a.go": "package a\n\nfunc F() int { return 1 }\n", "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = Undefined() }\n"},
			2, "`FAIL <pkg> [build failed]` from the test file: " + nonViableWhy},
		{"package fails to load",
			map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/nosuch\"\n\nfunc F() int { return 1 }\n", "a/a_test.go": passTest},
			2, "`FAIL <pkg> [setup failed]`: " + nonViableWhy},
		{"test genuinely fails",
			map[string]string{"a/a.go": "package a\n\nfunc F() int { return 1 }\n", "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F() != 2 {\n\t\tt.Fatal(\"F\")\n\t}\n}\n"},
			1, "a compiled mutant that a test catches is KILLED; mapping every failure to 2 would turn all kills into NOT VIABLE and drop them from the score"},
		{"failing test logs a build-failed line",
			map[string]string{"a/a.go": "package a\n\nfunc F() int { return 1 }\n", "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tt.Log(\"FAIL\\texample.com/m/a [build failed]\")\n\tt.Fatal(\"F\")\n}\n"},
			1, "the package built and a test failed, so the mutant is KILLED; only go's own per-package FAIL line marks a build failure, not text a test prints"},
		{"tests pass",
			map[string]string{"a/a.go": "package a\n\nfunc F() int { return 1 }\n", "a/a_test.go": passTest},
			0, "a mutant no test catches LIVES"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.26\n"})
			writeFiles(t, dir, c.files)
			code, out := wrappedGo(t, dir, "test", "-timeout", "60s", "-failfast", "example.com/m/a")
			if code != c.want {
				t.Fatalf("wrapped `go test -failfast example.com/m/a` exited %d, want %d; %s\n%s", code, c.want, c.why, out)
			}
		})
	}
	t.Run("go build of a package that fails to compile", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.26\n", "a/a.go": "package a\n\nfunc F() [2]int { return [1 - 1]int{} }\n"})
		if code, out := wrappedGo(t, dir, "build", "./a"); code != 1 {
			t.Fatalf("wrapped `go build` on a package that does not compile exited %d, want 1 as the real go does; only `go test` exit codes feed Gremlins' KILLED/NOT VIABLE mapping, and the wrapper is on PATH for everything Gremlins runs\n%s", code, out)
		}
	})
}

const arraySrc = "package a\n\nfunc F() [2]int {\n\treturn [2]int{}\n}\n"
const arrayTest = "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\t_ = F()\n}\n"

func TestMutationNonCompilingMutantIsNotKilled(t *testing.T) {
	needGremlins(t)
	dir := committedModule(t, map[string]string{"a/a.go": arraySrc, "a/a_test.go": arrayTest})
	writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(arraySrc, "[2]int{}", "[1 + 1]int{}", 1)})
	ms := mutationReport(t, dir)
	notViable := 0
	for _, m := range ms {
		if m.Status == "Killed" {
			t.Fatalf("mutant %+v reported Killed (all mutants: %+v); TestF asserts nothing, so the only way a mutant of `[1 + 1]int{}` fails is by not compiling ([0]int is not [2]int). %s", m, ms, nonViableWhy)
		}
		if m.Line == 4 && m.Status == "CompileError" {
			notViable++
		}
	}
	if notViable == 0 {
		t.Fatalf("no CompileError mutant on changed line 4: %+v; the `+` to `-` mutant of `[1 + 1]int{}` cannot compile and must be reported NOT VIABLE so the evaluator excludes it from the score", ms)
	}
}
