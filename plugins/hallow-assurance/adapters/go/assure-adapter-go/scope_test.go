package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func writeScope(t *testing.T, s adapterkit.Scope) string {
	t.Helper()
	entries := make([]map[string]any, len(s))
	for i, e := range s {
		entries[i] = map[string]any{"path": e.Path}
		if e.Ranges != nil {
			entries[i]["ranges"] = e.Ranges
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "scope.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func markingTools(t *testing.T) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "ran")
	body := `printf '%s\n' "$0 $*" >> '` + marker + "'\nexit 1\n"
	narrowPath(t, nil, map[string]string{"go": body, "git": body, "gremlins": body, "mise": body})
	return marker
}

func objectiveNames() []string {
	names := make([]string, 0, len(runners))
	for n := range runners {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func TestRunRejectsBothSelectorsWithoutRunningTools(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go"}})
	marker := markingTools(t)
	for _, obj := range objectiveNames() {
		out := filepath.Join(t.TempDir(), "ev")
		_, stderr, code := runIn(t, dir, "run", obj, "--changed-from", "HEAD", "--scope", scope, "--out", out)
		if code == 0 {
			t.Fatalf("%s with --changed-from and --scope exited 0; the two selectors name different change sets, and running either one silently drops the other", obj)
		}
		if ran, err := os.ReadFile(marker); err == nil {
			t.Fatalf("%s with both selectors ran tools before rejecting:\n%s\na usage error must cost nothing and change nothing", obj, ran)
		}
		if _, err := os.Stat(out); err == nil {
			t.Fatalf("%s with both selectors created %s; a rejected run must leave no evidence directory", obj, out)
		}
		if !strings.Contains(stderr, "usage:") {
			t.Fatalf("%s stderr %q; a rejected run must print the usage line naming both selectors", obj, stderr)
		}
	}
}

func TestFailOnBaseWithScopeIsUsageError(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go", Ranges: [][2]int{{1, 1}}}})
	marker := markingTools(t)
	out := filepath.Join(t.TempDir(), "ev")
	_, stderr, code := runIn(t, dir, "run", "VER-FAIL-ON-BASE", "--scope", scope, "--out", out)
	if code != 2 {
		t.Fatalf("VER-FAIL-ON-BASE --scope exit %d (stderr %q), want 2; fail-on-base reruns tests on a base commit, and a scope has none, so any other outcome reports evidence for a comparison that never happened", code, stderr)
	}
	if ran, err := os.ReadFile(marker); err == nil {
		t.Fatalf("VER-FAIL-ON-BASE --scope ran tools before refusing:\n%s", ran)
	}
	if !strings.Contains(stderr, "--changed-from") {
		t.Fatalf("stderr %q does not name --changed-from; the caller must learn which selector fail-on-base needs", stderr)
	}
}

var scopeCandidates = []string{"go.mod", "a/a.go", "a/b.go", "a/a_test.go", "b/b.go", "c/d/d.go", "README.md", "a/testdata/x.go"}

func TestScopedUnchangedFileSelectsAsIfChanged(t *testing.T) {
	files := map[string]string{}
	for _, p := range scopeCandidates[1:] {
		pkg := filepath.Base(filepath.Dir(p))
		files[p] = "package " + pkg + "\n"
	}
	files["README.md"] = "x\n"
	dir := committedModule(t, files)
	rapid.Check(t, func(rt *rapid.T) {
		picked := rapid.SliceOfNDistinct(rapid.SampledFrom(scopeCandidates), 1, len(scopeCandidates), rapid.ID[string]).Draw(rt, "scoped")
		var s adapterkit.Scope
		for _, p := range picked {
			s = append(s, adapterkit.ScopeEntry{Path: p})
		}
		paths, err := selectedPaths(dir, adapterkit.RunArgs{Scope: s})
		if err != nil {
			rt.Fatal(err)
		}
		scoped := selectPackages(dir, paths)
		orig := map[string][]byte{}
		for _, p := range picked {
			full := filepath.Join(dir, p)
			b, err := os.ReadFile(full)
			if err != nil {
				rt.Fatal(err)
			}
			orig[full] = b
			if err := os.WriteFile(full, append(slices.Clone(b), "\n// edited\n"...), 0o644); err != nil {
				rt.Fatal(err)
			}
		}
		changed, err := adapterkit.ChangedPaths(dir, "HEAD")
		for full, b := range orig {
			if werr := os.WriteFile(full, b, 0o644); werr != nil {
				rt.Fatal(werr)
			}
		}
		if err != nil {
			rt.Fatal(err)
		}
		if want := selectPackages(dir, changed); !reflect.DeepEqual(scoped, want) {
			rt.Fatalf("scope %v on a clean tree selects %v; editing the same files selects %v; a file listed in --scope must be tested exactly as if it had changed, or an audit of committed code skips the packages it names", picked, scoped, want)
		}
	})
}

type mutantKey struct {
	path string
	line int
}

func coveredLines(s adapterkit.Scope, maxLine int) map[mutantKey]bool {
	in := map[mutantKey]bool{}
	for _, e := range s {
		if e.Ranges == nil {
			for l := 1; l <= maxLine; l++ {
				in[mutantKey{e.Path, l}] = true
			}
		}
		for _, r := range e.Ranges {
			for l := r[0]; l <= r[1]; l++ {
				in[mutantKey{e.Path, l}] = true
			}
		}
	}
	return in
}

func TestKeepScopedKeepsExactlyInRangeMutants(t *testing.T) {
	const maxLine = 30
	paths := []string{"a/x.go", "a/y.go", "b/z.go"}
	rapid.Check(t, func(t *rapid.T) {
		rep := newStrykerReport()
		var all []strykerMutant
		for _, p := range paths {
			if !rapid.Bool().Draw(t, "present") {
				continue
			}
			f := &strykerFile{Language: "go", Source: "package x\n", Mutants: []strykerMutant{}}
			for i := range rapid.IntRange(0, 5).Draw(t, "nmut") {
				m := strykerMutant{ID: fmt.Sprintf("%s#%d", p, i), Status: "Survived"}
				m.Location.Start.Line = rapid.IntRange(1, maxLine).Draw(t, "line")
				f.Mutants = append(f.Mutants, m)
				all = append(all, m)
			}
			rep.Files[p] = f
		}
		var s adapterkit.Scope
		for range rapid.IntRange(1, 3).Draw(t, "nentries") {
			e := adapterkit.ScopeEntry{Path: rapid.SampledFrom(append(slices.Clone(paths), "c/none.go")).Draw(t, "path")}
			if !rapid.Bool().Draw(t, "wholeFile") {
				for range rapid.IntRange(1, 2).Draw(t, "nranges") {
					lo := rapid.IntRange(1, maxLine).Draw(t, "lo")
					e.Ranges = append(e.Ranges, [2]int{lo, rapid.IntRange(lo, maxLine).Draw(t, "hi")})
				}
			}
			s = append(s, e)
		}
		in := coveredLines(s, maxLine)
		want := map[string][]string{}
		for _, m := range all {
			p, _, _ := strings.Cut(m.ID, "#")
			if in[mutantKey{p, m.Location.Start.Line}] {
				want[p] = append(want[p], m.ID)
			}
		}
		rep.keepScoped(s)
		got := map[string][]string{}
		for p, f := range rep.Files {
			if len(f.Mutants) == 0 {
				t.Fatalf("file %s kept with no mutants; a file outside every range would still appear in the evidence as audited", p)
			}
			for _, m := range f.Mutants {
				got[p] = append(got[p], m.ID)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scope %+v kept %v, want %v; a mutant outside the scope's ranges is evidence about code the audit did not ask about, and a dropped in-range mutant hides a survivor", s, got, want)
		}
	})
}

func TestUnleashPassesDiffOnlyWithRef(t *testing.T) {
	if args := unleashArgv(t, ""); slices.Contains(args, "--diff") || slices.Contains(args, "") {
		t.Fatalf("gremlins argv %q for a scoped run; --diff with an empty ref makes Gremlins diff against nothing, so a scoped run must mutate the whole module and filter afterwards", args)
	}
	for _, ref := range []string{"HEAD", "origin/master", "3ef7621"} {
		if args := unleashArgv(t, ref); flagValue(args, "--diff") != ref {
			t.Fatalf("gremlins argv %q, want --diff %s; without it a --changed-from run mutates every line, not the change", args, ref)
		}
	}
}

func gremlinsJSONStub(argv string, report string) string {
	return `printf '%s\n' "$@" >| '` + argv + `'
while [ $# -gt 0 ]; do
	if [ "$1" = --output ]; then shift; printf '%s' '` + report + `' >| "$1"; fi
	shift
done
`
}

func TestScopedMutationKeepsOnlyInRangeSurvivors(t *testing.T) {
	src := "package a\n\nfunc A() int { return 1 }\n" + strings.Repeat("// line\n", 80)
	dir := committedModule(t, map[string]string{"a/a.go": src, "b/b.go": "package b\n"})
	argv := filepath.Join(t.TempDir(), "argv")
	report := `{"go_module":"example.com/m","files":[` +
		`{"file_name":"a/a.go","mutations":[{"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":10,"column":1},{"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":50,"column":1}]},` +
		`{"file_name":"b/b.go","mutations":[{"type":"ARITHMETIC_BASE","status":"LIVED","line":1,"column":1}]}]}`
	narrowPath(t, []string{"git"}, withStubs(gremlinsStubs(t, "0.6.0"), map[string]string{"gremlins": gremlinsJSONStub(argv, report)}))
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go", Ranges: [][2]int{{40, 72}}}})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-MUTATION-CHANGED", "--scope", scope, "--out", out)
	if code != 0 || resp.Environment != nil || len(resp.Evidence) != 1 {
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
	var lines []string
	for _, m := range ms {
		lines = append(lines, fmt.Sprintf("%s:%d", m.Path, m.Line))
	}
	if !slices.Equal(lines, []string{"a/a.go:50"}) {
		t.Fatalf("mutants %v, want only a/a.go:50; scope range [40,72] limits mutation evidence, so the survivor on line 10 and the other file's survivor are outside the audit", lines)
	}
	got, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("--diff")) {
		t.Fatalf("gremlins argv %q for a scoped run on an unmodified file; with --diff, Gremlins skips every unchanged line, so the scoped range is never mutated", got)
	}
}

func TestScopedTestsRunUnchangedPackage(t *testing.T) {
	dir := committedModule(t, map[string]string{
		"a/a.go": "package a\n", "a/a_test.go": failingTest,
		"b/b.go": "package b\n", "b/b_test.go": strings.Replace(strings.Replace(failingTest, "package a", "package b", 1), "TestNo", "TestB", 1),
	})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go", Ranges: [][2]int{{1, 1}}}})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--scope", scope, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	if j.Failures != 1 || len(j.Failing) != 1 || !strings.Contains(j.Failing[0].Name, "TestNo") {
		t.Fatalf("JUnit %+v; the scoped package a's failing TestNo must run though a/a.go is unchanged, and package b, outside the scope, must not", j)
	}
}

func TestScopedCoverageCoversUnchangedPackage(t *testing.T) {
	dir := committedModule(t, map[string]string{
		"a/a.go": signSrc, "a/a_test.go": signTest,
		"b/b.go": "package b\n\nfunc B() int { return 1 }\n",
	})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go", Ranges: [][2]int{{4, 5}}}})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-COVERAGE-RESOLUTION", "--scope", scope, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	files, err := ev.ParseLCOV(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "a/a.go" {
		t.Fatalf("LCOV records %+v; a scoped run covers the scoped package, unchanged or not, and nothing outside it", files)
	}
	if h, ok := files[0].Lines[5]; !ok || h != 0 {
		t.Fatalf("DA for untested line 5 = %d,%v; want a zero-hit record, or the audit cannot see the uncovered branch in its range", h, ok)
	}
}

func TestScopedFuzzSelectsScopedSourcePackage(t *testing.T) {
	dir := committedModule(t, map[string]string{
		"a/a.go": parseSrc, "a/a_test.go": fuzzTargetSrc,
		"b/b.go": "package b\n\nfunc B() int { return 1 }\n",
	})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go"}})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-ROBUST-FUZZ", "--scope", scope, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Suites) != 1 || !slices.Equal(sourceProps(j.Suites[0]), []string{"a/a.go"}) ||
		len(j.Suites[0].Cases) != 1 || j.Suites[0].Cases[0].Name != "a.FuzzParse" || j.Suites[0].Cases[0].Outcome != ev.Passed {
		t.Fatalf("suites %+v; a scoped run fuzzes the scoped source's package and attributes it to that source, though the file is unchanged", j.Suites)
	}
}
