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
	if len(calls) != 1 || calls[0].dir != dir || calls[0].args[len(calls[0].args)-1] != "./a" {
		t.Fatalf("gremlins calls %+v, want one call on ./a in %s; mutating the whole module for a one-file scope took over 30 minutes on this repository, against 66 s for the scoped package", calls, dir)
	}
	if slices.Contains(calls[0].args, "--diff") {
		t.Fatalf("gremlins argv %q for a scoped run on an unmodified file; with --diff, Gremlins skips every unchanged line, so the scoped range is never mutated", calls[0].args)
	}
}

// twoModuleRepo is a git repository with sibling modules m1 and m2 and no module at its root.
// m1/root.go is a package at its module root; m1/a/sub nests under m1/a; m1/ab shares m1/a's
// name as a string prefix without nesting under it.
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
	"m1/ab/ab.go", "m1/b/b.go", "m1/internal/core/core.go", "m2/go.mod", "m2/x.go", "m2/c/c.go", "README.md",
}

type fataler interface {
	Fatal(...any)
	Fatalf(string, ...any)
}

func callKeys(t fataler, root string, calls []gremlinsCall, ref string) []string {
	keys := make([]string, 0, len(calls))
	for _, c := range calls {
		mod, err := filepath.Rel(root, c.dir)
		if err != nil || strings.HasPrefix(mod, "..") {
			t.Fatalf("gremlins ran in %s, outside the repository %s", c.dir, root)
		}
		if got := flagValue(c.args, "--diff"); got != ref || (ref == "" && slices.Contains(c.args, "--diff")) {
			t.Fatalf("gremlins argv %q in %s: --diff %q, want %q; a scoped run must not diff (unchanged scoped lines would be skipped) and a --changed-from run must (or it mutates every line)", c.args, mod, got, ref)
		}
		keys = append(keys, filepath.ToSlash(mod)+" "+path.Clean(c.args[len(c.args)-1]))
	}
	slices.Sort(keys)
	return keys
}

// mutateWith runs runMutation on root as the adapter would for ra, against the stub logging to
// log, and returns the module-relative targets Gremlins ran on per module, and the mutants
// reported as "path:line".
func mutateWith(t fataler, root, log, evRoot string, ra adapterkit.RunArgs) (map[string][]string, map[string]bool) {
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
	targets := map[string][]string{}
	for _, k := range callKeys(t, root, readGremlinsCalls(t, log), ra.ChangedFrom) {
		mod, target, _ := strings.Cut(k, " ")
		targets[mod] = append(targets[mod], target)
	}
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
	return targets, got
}

// scopeOracle reads a scope over twoModuleRepo: the scoped packages per module (module-relative,
// "." for the module root), the modules whose go.mod or go.sum is scoped, and the stub's mutants
// (lines 1 and 3 of every non-test file) that the scope's ranges contain.
func scopeOracle(s adapterkit.Scope) (pkgs map[string]map[string]bool, whole, mutants map[string]bool) {
	pkgs, whole, mutants = map[string]map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, p := range s.Paths() {
		mod, rel, _ := strings.Cut(p, "/")
		switch {
		case rel == "go.mod" || rel == "go.sum":
			whole[mod] = true
		case strings.HasSuffix(p, ".go"):
			if pkgs[mod] == nil {
				pkgs[mod] = map[string]bool{}
			}
			pkgs[mod][path.Dir(rel)] = true
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
	return pkgs, whole, mutants
}

// under reports whether package p lies in the directory Gremlins walks for target tg.
func under(p, tg string) bool {
	return tg == "." || tg == p || strings.HasPrefix(p, tg+"/")
}

func checkScopedTargets(t fataler, s adapterkit.Scope, targets map[string][]string) {
	pkgs, whole, _ := scopeOracle(s)
	for mod, ts := range targets {
		if !whole[mod] && pkgs[mod] == nil {
			t.Fatalf("scope %v ran gremlins in module %s on %v; nothing in that module is scoped", s.Paths(), mod, ts)
		}
	}
	for mod := range whole {
		if !slices.Equal(targets[mod], []string{"."}) {
			t.Fatalf("scope %v with %s's go.mod or go.sum ran gremlins there on %v, want one run on .; a scoped module config can change every package, so the module is mutated once, whole", s.Paths(), mod, targets[mod])
		}
	}
	for mod, set := range pkgs {
		if whole[mod] {
			continue
		}
		for _, tg := range targets[mod] {
			if !set[tg] {
				t.Fatalf("scope %v ran gremlins in %s on %s, which is not a scoped package (scoped: %v); a whole-module run per scope took over 30 minutes on this repository against 66 s for the scoped package", s.Paths(), mod, tg, set)
			}
		}
		for p := range set {
			n := 0
			for _, tg := range targets[mod] {
				if under(p, tg) {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("scope %v: scoped package %s/%s lies under %d of the gremlins targets %v, want exactly 1; Gremlins walks its target recursively, so zero leaves the package unmutated and two mutate it twice, duplicating its mutants", s.Paths(), mod, p, n, targets[mod])
			}
		}
	}
}

func drawScope(t *rapid.T) adapterkit.Scope {
	picked := rapid.SliceOfNDistinct(rapid.SampledFrom(twoModuleScopeCandidates), 1, len(twoModuleScopeCandidates), rapid.ID[string]).Draw(t, "scoped")
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

func TestScopedMutationRunsGremlinsPerScopedPackage(t *testing.T) {
	root := twoModuleRepo(t)
	log := filepath.Join(t.TempDir(), "calls")
	narrowPath(t, []string{"git", "find"}, map[string]string{"gremlins": gremlinsWalkStub(log, 1, 3)})
	evRoot := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		s := drawScope(rt)
		targets, got := mutateWith(rt, root, log, evRoot, adapterkit.RunArgs{Scope: s})
		checkScopedTargets(rt, s, targets)
		if _, _, want := scopeOracle(s); !reflect.DeepEqual(got, want) {
			rt.Fatalf("scope %+v reported mutants %v, want %v; Gremlins names files relative to the package it was given, so each name must be rejoined with module and package to its repo path, then cut to the scope's ranges", s, got, want)
		}
	})
}

func TestScopedMutationTargetsOutermostPackages(t *testing.T) {
	root := twoModuleRepo(t)
	log := filepath.Join(t.TempDir(), "calls")
	narrowPath(t, []string{"git", "find"}, map[string]string{"gremlins": gremlinsWalkStub(log, 1, 3)})
	evRoot := t.TempDir()
	for _, c := range []struct {
		scoped []string
		want   []string
		why    string
	}{
		{[]string{"m1/a/a.go", "m1/a/sub/s.go"}, []string{"a"}, "Gremlins on ./a already walks a/sub"},
		{[]string{"m1/root.go", "m1/a/a.go"}, []string{"."}, "the module-root package's run walks every package in the module"},
		{[]string{"m1/root.go", "m1/a/a.go", "m1/a/sub/s.go", "m1/internal/core/core.go"}, []string{"."}, "the module-root package's run walks every package in the module"},
		{[]string{"m1/a/a.go", "m1/ab/ab.go"}, []string{"a", "ab"}, "ab is a sibling of a, not nested under it, so ./a does not walk it"},
	} {
		var s adapterkit.Scope
		for _, p := range c.scoped {
			s = append(s, adapterkit.ScopeEntry{Path: p})
		}
		targets, _ := mutateWith(t, root, log, evRoot, adapterkit.RunArgs{Scope: s})
		if len(targets) != 1 || !slices.Equal(targets["m1"], c.want) {
			t.Fatalf("scope %v ran gremlins on %v, want m1 targets %v; %s", c.scoped, targets, c.want, c.why)
		}
	}
}

// editFiles appends a declaration to each path under root and returns a function restoring them.
func editFiles(t fataler, root string, paths []string) func() {
	orig := map[string][]byte{}
	restore := func() {
		for full, b := range orig {
			_ = os.WriteFile(full, b, 0o644)
		}
	}
	for _, p := range paths {
		full := filepath.Join(root, p)
		b, err := os.ReadFile(full)
		if err != nil {
			restore()
			t.Fatal(err)
		}
		orig[full] = b
		if err := os.WriteFile(full, append(slices.Clone(b), "\nvar _ = 1\n"...), 0o644); err != nil {
			restore()
			t.Fatal(err)
		}
	}
	return restore
}

func TestRefMutationRunsGremlinsOncePerModuleWithDiff(t *testing.T) {
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
		edited := rapid.SliceOfNDistinct(rapid.SampledFrom(goFiles), 1, len(goFiles), rapid.ID[string]).Draw(rt, "edited")
		defer editFiles(rt, root, edited)()
		want := map[string][]string{}
		for _, p := range edited {
			mod, _, _ := strings.Cut(p, "/")
			want[mod] = []string{"."}
		}
		targets, got := mutateWith(rt, root, log, evRoot, adapterkit.RunArgs{ChangedFrom: "HEAD"})
		if !reflect.DeepEqual(targets, want) {
			rt.Fatalf("edits to %v ran gremlins on %v, want %v; a --changed-from run lets Gremlins' --diff pick the changed lines, so it runs once per touched module on the whole module", edited, targets, want)
		}
		for _, p := range edited {
			if !strings.HasSuffix(p, "_test.go") && !got[p+":1"] {
				rt.Fatalf("edited %s has no mutant in %v; Gremlins names files relative to the module in a whole-module run, so they must be rejoined with the module directory alone", p, got)
			}
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
