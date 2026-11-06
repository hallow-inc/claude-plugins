package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
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
	for _, target := range []string{".", "./a", "./internal/core"} {
		args := unleashArgv(t, "", target)
		if slices.Contains(args, "--diff") || slices.Contains(args, "") {
			t.Fatalf("gremlins argv %q for a scoped run; --diff with an empty ref makes Gremlins diff against nothing, so a scoped run must mutate its packages whole and filter afterwards", args)
		}
		if args[len(args)-1] != target {
			t.Fatalf("gremlins argv %q, want %s last; Gremlins mutates the package named by its final argument, so any other last argument mutates code the run was not asked about", args, target)
		}
	}
	for _, ref := range []string{"HEAD", "origin/master", "3ef7621"} {
		if args := unleashArgv(t, ref, "."); flagValue(args, "--diff") != ref {
			t.Fatalf("gremlins argv %q, want --diff %s; without it a --changed-from run mutates every line, not the change", args, ref)
		}
	}
}

type gremlinsCall struct {
	dir  string
	args []string
}

// gremlinsWalkStub mimics Gremlins v0.6.0: it walks its last argument recursively and names
// each non-test .go file relative to that argument, as `gremlins unleash ./a` reports "sub/s.go"
// for a/sub/s.go. Every file gets one LIVED mutant per line in lines. Each call appends
// "cwd\targ\targ..." to log.
func gremlinsWalkStub(log string, lines ...int) string {
	muts := make([]string, len(lines))
	for i, l := range lines {
		muts[i] = fmt.Sprintf(`{"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":%d,"column":1}`, l)
	}
	return `rec=$(pwd -P)
for a in "$@"; do rec="$rec	$a"; done
printf '%s\n' "$rec" >> '` + log + `'
out=; prev=; target=
for a in "$@"; do
	if [ "$prev" = --output ]; then out=$a; fi
	prev=$a; target=$a
done
cd "$target" || exit 3
sep=
{
	printf '{"go_module":"example.com/m","files":['
	for f in $(find . -name '*.go' ! -name '*_test.go'); do
		printf '%s{"file_name":"%s","mutations":[` + strings.Join(muts, ",") + `]}' "$sep" "${f#./}"
		sep=,
	done
	printf ']}'
} >| "$out"
`
}

func readGremlinsCalls(t interface{ Fatal(...any) }, log string) []gremlinsCall {
	data, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var calls []gremlinsCall
	for l := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		if l == "" {
			continue
		}
		f := strings.Split(l, "\t")
		calls = append(calls, gremlinsCall{dir: f[0], args: f[1:]})
	}
	return calls
}

func TestScopedMutationKeepsOnlyInRangeSurvivors(t *testing.T) {
	src := "package a\n\nfunc A() int { return 1 }\n" + strings.Repeat("// line\n", 80)
	dir := committedModule(t, map[string]string{"a/a.go": src, "b/b.go": "package b\n"})
	log := filepath.Join(t.TempDir(), "calls")
	narrowPath(t, []string{"git", "find"}, withStubs(gremlinsStubs(t, "0.6.0"), map[string]string{"gremlins": gremlinsWalkStub(log, 10, 50)}))
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
		t.Fatalf("mutants %v, want only a/a.go:50; scope range [40,72] limits mutation evidence, so the survivor on line 10 is outside the audit, and the package-relative name a.go must land at its repo path", lines)
	}
	calls := readGremlinsCalls(t, log)
	if dirs := callDirs(t, dir, calls, ""); !slices.Equal(dirs, []string{"a"}) {
		t.Fatalf("gremlins calls %+v walked %v, want one call on package a; mutating the whole module for a one-file scope took over 30 minutes on this repository, against 66 s for the scoped package", calls, dirs)
	}
	if want := filepath.Join(dir, "a"); calls[0].dir != want {
		t.Fatalf("gremlins ran in %s, want %s; run from the module root, Gremlins tests a package main mutant with the root package's tests", calls[0].dir, want)
	}
}

// twoModuleRepo is a git repository with sibling modules m1 and m2 and no module at its root.
// m1/root.go is a package at its module root; m1/a/sub nests under m1/a; m1/ab shares m1/a's
// name as a string prefix without nesting under it. m1/testdata and m1/vendor hold .go files the
// go tool never builds. m1/gen holds only a `//go:build ignore` generator and m1/plan9 only a
// _plan9.go file: build constraints exclude every .go file in each, so neither is a package.
func twoModuleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	src := func(pkg string) string { return "package " + pkg + "\n\nfunc F() {}\n" }
	writeFiles(t, dir, map[string]string{
		"m1/go.mod": "module example.com/m1\n\ngo 1.26\n", "m1/go.sum": "",
		"m1/root.go": src("m1"), "m1/a/a.go": src("a"), "m1/a/a2.go": src("a"), "m1/a/a_test.go": src("a"),
		"m1/a/sub/s.go": src("sub"), "m1/ab/ab.go": src("ab"), "m1/b/b.go": src("b"), "m1/internal/core/core.go": src("core"),
		"m1/testdata/t.go": src("t"), "m1/vendor/v/v.go": src("v"),
		"m1/gen/gen.go": "//go:build ignore\n\npackage main\n\nfunc main() {}\n", "m1/plan9/p_plan9.go": src("plan9"),
		"m2/go.mod": "module example.com/m2\n\ngo 1.26\n", "m2/x.go": src("m2"), "m2/c/c.go": src("c"),
		"README.md": "x\n",
	})
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

var twoModuleScopeCandidates = []string{
	"m1/go.mod", "m1/go.sum", "m1/root.go", "m1/a/a.go", "m1/a/a2.go", "m1/a/a_test.go", "m1/a/sub/s.go",
	"m1/ab/ab.go", "m1/b/b.go", "m1/internal/core/core.go", "m1/testdata/t.go", "m1/vendor/v/v.go",
	"m1/gen/gen.go", "m1/plan9/p_plan9.go", "m2/go.mod", "m2/x.go", "m2/c/c.go", "README.md",
}

// twoModuleSources lists twoModuleRepo's non-test .go files that the go tool builds.
var twoModuleSources = []string{
	"m1/root.go", "m1/a/a.go", "m1/a/a2.go", "m1/a/sub/s.go", "m1/ab/ab.go", "m1/b/b.go",
	"m1/internal/core/core.go", "m2/x.go", "m2/c/c.go",
}

// constraintExcluded lists twoModuleRepo's .go files that build constraints exclude on every
// GOOS and GOARCH the adapter runs on.
var constraintExcluded = []string{"m1/gen/gen.go", "m1/plan9/p_plan9.go"}

func builtByGo(p string) bool {
	return strings.HasSuffix(p, ".go") && !strings.Contains(p, "/testdata/") && !strings.Contains(p, "/vendor/") &&
		!slices.Contains(constraintExcluded, p)
}

// sourcesIn returns the twoModuleSources files directly in one of dirs, each with the stub's
// mutant on line 1.
func sourcesIn(dirs []string) map[string]bool {
	got := map[string]bool{}
	for _, f := range twoModuleSources {
		if slices.Contains(dirs, path.Dir(f)) {
			got[f+":1"] = true
		}
	}
	return got
}

type fataler interface {
	Fatal(...any)
	Fatalf(string, ...any)
}

// callDirs returns, sorted, the repository-relative directory each Gremlins call walked: its
// last argument, resolved against the call's working directory.
func callDirs(t fataler, root string, calls []gremlinsCall, ref string) []string {
	dirs := make([]string, 0, len(calls))
	for _, c := range calls {
		if got := flagValue(c.args, "--diff"); got != ref || (ref == "" && slices.Contains(c.args, "--diff")) {
			t.Fatalf("gremlins argv %q in %s: --diff %q, want %q; a scoped run must not diff (unchanged scoped lines would be skipped) and a --changed-from run must (or it mutates every line)", c.args, c.dir, got, ref)
		}
		walked := c.args[len(c.args)-1]
		if !filepath.IsAbs(walked) {
			walked = filepath.Join(c.dir, walked)
		}
		rel, err := filepath.Rel(root, walked)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			t.Fatalf("gremlins in %s walked %s, outside the repository %s", c.dir, walked, root)
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	slices.Sort(dirs)
	return dirs
}

// mutateWith runs runMutation on root as the adapter would for ra, against the stub logging to
// log, and returns the repository-relative directories Gremlins walked, one entry per call, and
// the mutants reported as "path:line".
func mutateWith(t fataler, root, log, evRoot string, ra adapterkit.RunArgs) ([]string, map[string]bool) {
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(evRoot, "ev")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := selectedPaths(root, ra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runMutation(root, ra, out, selectPackages(root, paths), nil); err != nil {
		t.Fatal(err)
	}
	dirs := callDirs(t, root, readGremlinsCalls(t, log), ra.ChangedFrom)
	data, err := os.ReadFile(filepath.Join(out, "mutation.json"))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := ev.ParseMutationReport(data)
	if err != nil {
		t.Fatalf("%v; the mutation-report schema forbids a mutant listed twice, so an overlapping Gremlins run makes the evidence unusable", err)
	}
	got := map[string]bool{}
	for _, m := range ms {
		got[fmt.Sprintf("%s:%d", m.Path, m.Line)] = true
	}
	return dirs, got
}

// scopeOracle reads a scope over twoModuleRepo: the package directories Gremlins must run in
// (repository-relative, sorted), and the stub's mutants (lines 1 and 3 of every built non-test
// file) that the scope's ranges contain. A scoped .go file the go tool builds selects its own
// directory; a scoped go.mod or go.sum selects every package directory of its module. A
// directory whose .go files build constraints all exclude is no package and is never selected.
func scopeOracle(s adapterkit.Scope) (runs []string, mutants map[string]bool) {
	set, mutants := map[string]bool{}, map[string]bool{}
	for _, p := range s.Paths() {
		mod, rel, _ := strings.Cut(p, "/")
		switch {
		case rel == "go.mod" || rel == "go.sum":
			for _, f := range twoModuleSources {
				if strings.HasPrefix(f, mod+"/") {
					set[path.Dir(f)] = true
				}
			}
		case builtByGo(p):
			set[path.Dir(p)] = true
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			for _, l := range []int{1, 3} {
				if s.Contains(p, l) {
					mutants[fmt.Sprintf("%s:%d", p, l)] = true
				}
			}
		}
	}
	for d := range set {
		runs = append(runs, d)
	}
	slices.Sort(runs)
	return runs, mutants
}

func drawScope(t *rapid.T, candidates []string) adapterkit.Scope {
	picked := rapid.SliceOfNDistinct(rapid.SampledFrom(candidates), 1, len(candidates), rapid.ID[string]).Draw(t, "scoped")
	s := make(adapterkit.Scope, 0, len(picked))
	for _, p := range picked {
		e := adapterkit.ScopeEntry{Path: p}
		if rapid.Bool().Draw(t, "ranged") {
			lo := rapid.IntRange(1, 3).Draw(t, "lo")
			e.Ranges = [][2]int{{lo, rapid.IntRange(lo, 3).Draw(t, "hi")}}
		}
		s = append(s, e)
	}
	return s
}

func checkScopedRuns(t fataler, s adapterkit.Scope, dirs []string, got map[string]bool) {
	want, mutants := scopeOracle(s)
	if !slices.Equal(dirs, want) {
		t.Fatalf("scope %v ran gremlins on %v, want one run on each of %v; each scoped package is mutated by its own run so that its mutants are judged by its own tests (a package main nested under a walked directory is otherwise judged by the outer package's tests), a scoped go.mod or go.sum can change every package of its module, a directory whose every .go file build constraints exclude (m1/gen's `//go:build ignore` generator, m1/plan9's _plan9.go) is no package and Gremlins v0.6.0 exits 1 there (\"no packages to test\"), failing VER-MUTATION-CHANGED for the whole module, and a run anywhere else costs time for evidence the audit did not ask about", s.Paths(), dirs, want)
	}
	if !reflect.DeepEqual(got, mutants) {
		t.Fatalf("scope %+v reported mutants %v, want %v; Gremlins names files relative to the directory it walks, so each name must be rejoined with that directory to its repo path, files below it belong to another package's run, and the result is cut to the scope's ranges", s, got, mutants)
	}
}

func TestScopedMutationRunsGremlinsPerScopedPackage(t *testing.T) {
	root := twoModuleRepo(t)
	log := filepath.Join(t.TempDir(), "calls")
	narrowPath(t, []string{"git", "find"}, map[string]string{"gremlins": gremlinsWalkStub(log, 1, 3)})
	evRoot := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		s := drawScope(rt, twoModuleScopeCandidates)
		dirs, got := mutateWith(rt, root, log, evRoot, adapterkit.RunArgs{Scope: s})
		checkScopedRuns(rt, s, dirs, got)
	})
}

func TestScopedNestedPackagesEachGetTheirOwnRun(t *testing.T) {
	root := twoModuleRepo(t)
	log := filepath.Join(t.TempDir(), "calls")
	narrowPath(t, []string{"git", "find"}, map[string]string{"gremlins": gremlinsWalkStub(log, 1, 3)})
	evRoot := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		s := drawScope(rt, []string{"m1/root.go", "m1/a/a.go", "m1/a/sub/s.go", "m1/ab/ab.go"})
		dirs, got := mutateWith(rt, root, log, evRoot, adapterkit.RunArgs{Scope: s})
		checkScopedRuns(rt, s, dirs, got)
	})
}

// changeFiles appends a declaration to each path in edited and deletes each path in deleted,
// under root, and returns a function restoring them.
func changeFiles(t fataler, root string, edited, deleted []string) func() {
	orig := map[string][]byte{}
	restore := func() {
		for full, b := range orig {
			_ = os.WriteFile(full, b, 0o644)
		}
	}
	for _, p := range append(slices.Clone(edited), deleted...) {
		full := filepath.Join(root, p)
		b, err := os.ReadFile(full)
		if err != nil {
			restore()
			t.Fatal(err)
		}
		orig[full] = b
		if slices.Contains(deleted, p) {
			err = os.Remove(full)
		} else {
			err = os.WriteFile(full, append(slices.Clone(b), "\nvar _ = 1\n"...), 0o644)
		}
		if err != nil {
			restore()
			t.Fatal(err)
		}
	}
	return restore
}

func TestRefMutationRunsGremlinsOncePerChangedPackageWithDiff(t *testing.T) {
	root := twoModuleRepo(t)
	log := filepath.Join(t.TempDir(), "calls")
	narrowPath(t, []string{"git", "find"}, map[string]string{"gremlins": gremlinsWalkStub(log, 1)})
	var goFiles []string
	for _, p := range twoModuleScopeCandidates {
		if strings.HasSuffix(p, ".go") {
			goFiles = append(goFiles, p)
		}
	}
	evRoot := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		changed := rapid.SliceOfNDistinct(rapid.SampledFrom(twoModuleScopeCandidates), 1, len(twoModuleScopeCandidates), rapid.ID[string]).Draw(rt, "changed")
		var edited, deleted []string
		for _, p := range changed {
			if slices.Contains(goFiles, p) && rapid.Bool().Draw(rt, "delete "+p) {
				deleted = append(deleted, p)
			} else {
				edited = append(edited, p)
			}
		}
		defer changeFiles(rt, root, edited, deleted)()
		var want []string
		for _, p := range edited {
			if builtByGo(p) && !strings.HasSuffix(p, "_test.go") && !slices.Contains(want, path.Dir(p)) {
				want = append(want, path.Dir(p))
			}
		}
		slices.Sort(want)
		dirs, got := mutateWith(rt, root, log, evRoot, adapterkit.RunArgs{ChangedFrom: "HEAD"})
		if !slices.Equal(dirs, want) {
			rt.Fatalf("edits to %v and deletion of %v ran gremlins on %v, want one run on each of %v; a --changed-from run mutates each package directory holding a changed, existing, non-test .go file the go tool builds, alone, so a package main is judged by its own tests, and a change to tests, go.mod, go.sum, testdata, vendor, or a deleted file leaves no line for --diff to mutate, and a directory whose every .go file build constraints exclude (m1/gen's `//go:build ignore` generator, m1/plan9's _plan9.go) is no package: Gremlins v0.6.0 exits 1 there (\"no packages to test\"), failing VER-MUTATION-CHANGED for the whole module", edited, deleted, dirs, want)
		}
		live := map[string]bool{}
		for f := range sourcesIn(want) {
			if !slices.Contains(deleted, strings.TrimSuffix(f, ":1")) {
				live[f] = true
			}
		}
		if !reflect.DeepEqual(got, live) {
			rt.Fatalf("edits to %v and deletion of %v reported mutants %v, want %v; Gremlins names files relative to the package directory it walks, so each name must be rejoined with that directory, and files in nested directories belong to their own package's run or none", edited, deleted, got, live)
		}
	})
}

func TestScopedNestedPackagesReportEachMutantOnce(t *testing.T) {
	needGremlins(t)
	dir := committedModule(t, map[string]string{
		"a/a.go": boundarySrc, "a/a_test.go": boundaryTest,
		"a/sub/s.go":      strings.Replace(boundarySrc, "package a", "package sub", 1),
		"a/sub/s_test.go": strings.Replace(boundaryTest, "package a", "package sub", 1),
	})
	scope := writeScope(t, adapterkit.Scope{{Path: "a/a.go"}, {Path: "a/sub/s.go"}})
	out := filepath.Join(t.TempDir(), "ev")
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
			t.Fatalf("mutant %s reported %d times in %v; Gremlins walks its target directory recursively, so a run on ./a also mutates a/sub, and a second run on ./a/sub repeats it: a duplicated survivor counts twice against the mutation score", id, n, seen)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no mutants reported for two scoped files with mutable lines")
	}
	if _, err := ev.ParseMutationReport(data); err != nil {
		t.Fatalf("real Gremlins report rejected: %v", err)
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
