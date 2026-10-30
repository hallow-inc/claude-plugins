package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const scopeAdapterScript = `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":2,"languages":["LANG"],"claims":["**/*.LANG"],"patterns":{"test":["**/*_test.LANG"]},"objectives":{}}' ;;
classify)
  shift
  printf '%s' '{"protocol":0,"files":['
  sep=
  for p in "$@"; do
    case "$p" in
    *_test.LANG) role=test ;;
    *.gen.LANG) role=generated ;;
    *.cfg.LANG) role=config ;;
    *.LANG) role=source ;;
    *) continue ;;
    esac
    printf '%s{"path":"%s","language":"LANG","role":"%s"}' "$sep" "$p" "$role"
    sep=,
  done
  printf '%s' ']}'
  ;;
*) echo "assure-adapter-LANG: unexpected $*" >&2; exit 1 ;;
esac
`

const scopeManifest = "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: B\ncomponents: []\n"

func scopeAdapters(t fataler, langs ...string) string {
	t.Helper()
	bin, err := os.MkdirTemp("", "assure-scope-bin")
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, lang := range langs {
		script := strings.ReplaceAll(scopeAdapterScript, "LANG", lang)
		if err := os.WriteFile(filepath.Join(bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
			t.Fatalf("%v", err)
		}
	}
	return bin
}

func scopeClaim(rel string) (lang, role string) {
	switch {
	case strings.HasSuffix(rel, "_test.xx"):
		return "xx", "test"
	case strings.HasSuffix(rel, ".gen.xx"):
		return "xx", "generated"
	case strings.HasSuffix(rel, ".cfg.xx"):
		return "xx", "config"
	case strings.HasSuffix(rel, ".xx"):
		return "xx", "source"
	case strings.HasSuffix(rel, "_test.yy"):
		return "yy", "test"
	case strings.HasSuffix(rel, ".yy"):
		return "yy", "source"
	}
	return "", ""
}

type scopeTreeFile struct {
	rel       string
	lines     int
	trailing  bool
	committed bool
	ignored   bool
}

func (f scopeTreeFile) eligible() bool {
	_, role := scopeClaim(f.rel)
	return (role == "source" || role == "test") && (f.committed || !f.ignored)
}

func (f scopeTreeFile) content() string {
	ls := make([]string, f.lines)
	for i := range ls {
		ls[i] = fmt.Sprintf("line %d of %s", i+1, f.rel)
	}
	s := strings.Join(ls, "\n")
	if f.trailing {
		s += "\n"
	}
	return s
}

var scopeTreePaths = func() []string {
	var out []string
	for _, d := range []string{"", "a/", "a/b/", "c/"} {
		for _, stem := range []string{"m", "n"} {
			for _, kind := range []string{".xx", "_test.xx", ".gen.xx", ".cfg.xx", ".yy", "_test.yy", ".md"} {
				out = append(out, d+stem+kind)
			}
		}
	}
	return out
}()

type scopeTree struct {
	gitRoot string
	root    string
	files   []scopeTreeFile
	dirs    []string
	outside bool
}

func (tr scopeTree) byRel(rel string) (scopeTreeFile, bool) {
	for _, f := range tr.files {
		if f.rel == rel {
			return f, true
		}
	}
	return scopeTreeFile{}, false
}

func buildScopeTree(t fataler, gitRoot, sub string, files []scopeTreeFile) scopeTree {
	t.Helper()
	root := filepath.Join(gitRoot, filepath.FromSlash(sub))
	tr := scopeTree{gitRoot: gitRoot, root: root, files: files, outside: sub != ""}
	putFile(t, root, core.ManifestName, scopeManifest)
	gitIn(t, gitRoot, "init", "-q")
	var ignore strings.Builder
	var add []string
	dirs := map[string]bool{"": true}
	for _, f := range files {
		putFile(t, root, f.rel, f.content())
		if f.ignored {
			ignore.WriteString("/" + f.rel + "\n")
		}
		if f.committed {
			add = append(add, filepath.FromSlash(sub+f.rel))
		}
		for d := filepath.ToSlash(filepath.Dir(f.rel)); d != "."; d = filepath.ToSlash(filepath.Dir(d)) {
			dirs[d+"/"] = true
		}
	}
	putFile(t, root, ".gitignore", ignore.String())
	if tr.outside {
		putFile(t, gitRoot, "outside.xx", "one\ntwo\n")
		add = append(add, "outside.xx")
	}
	if len(add) > 0 {
		gitIn(t, gitRoot, append([]string{"add", "-f", "--"}, add...)...)
		gitIn(t, gitRoot, "-c", "commit.gpgsign=false", "-c", "core.excludesFile="+os.DevNull, "commit", "-qm", "base")
	}
	for d := range dirs {
		tr.dirs = append(tr.dirs, d)
	}
	slices.Sort(tr.dirs)
	return tr
}

type scopeHit struct {
	whole bool
	lines map[int]bool
}

type scopeArg struct {
	arg  string
	bad  bool
	hits map[string]scopeHit
}

func (tr scopeTree) relFromCwd(t *rapid.T, cwd, abs string) string {
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return filepath.ToSlash(rel)
}

func (tr scopeTree) wholeArg(arg string, match func(scopeTreeFile) bool) scopeArg {
	a := scopeArg{arg: arg, hits: map[string]scopeHit{}}
	for _, f := range tr.files {
		if f.eligible() && match(f) {
			a.hits[f.rel] = scopeHit{whole: true}
		}
	}
	a.bad = len(a.hits) == 0
	return a
}

var scopeGlobs = []string{"**", "**/*.xx", "**/*_test.*", "a/**", "a/*", "*.yy", "c/m*", "?/*.xx", "[ac]/**", "nomatch/**/*.xx", "mod/**", "a/[", "**/*.md", "*"}

var scopeBadRanges = []string{"", "3", "-", "1-", "-2", "a-b", "1-2-3", "1 - 2", "x", "1-x"}

func (tr scopeTree) pickFile(t *rapid.T, cwd string) (scopeTreeFile, string, bool) {
	if tr.outside && rapid.IntRange(0, 5).Draw(t, "outside") == 0 {
		return scopeTreeFile{rel: "outside.xx", lines: 2}, tr.relFromCwd(t, cwd, filepath.Join(tr.gitRoot, "outside.xx")), false
	}
	f := rapid.SampledFrom(tr.files).Draw(t, "file")
	return f, tr.relFromCwd(t, cwd, filepath.Join(tr.root, filepath.FromSlash(f.rel))), true
}

func (tr scopeTree) fileArg(t *rapid.T, cwd string) scopeArg {
	f, arg, inside := tr.pickFile(t, cwd)
	if !inside {
		return scopeArg{arg: arg, bad: true}
	}
	return tr.wholeArg(arg, func(g scopeTreeFile) bool { return g.rel == f.rel })
}

func (tr scopeTree) dirArg(t *rapid.T, cwd string) scopeArg {
	if tr.outside && rapid.IntRange(0, 5).Draw(t, "outside dir") == 0 {
		return scopeArg{arg: tr.relFromCwd(t, cwd, tr.gitRoot), bad: true}
	}
	d := rapid.SampledFrom(tr.dirs).Draw(t, "dir")
	arg := tr.relFromCwd(t, cwd, filepath.Join(tr.root, filepath.FromSlash(d)))
	return tr.wholeArg(arg, func(g scopeTreeFile) bool { return strings.HasPrefix(g.rel, d) })
}

func drawLine(t *rapid.T, n int, label string) int {
	return rapid.OneOf(rapid.SampledFrom([]int{0, 1, n, n + 1}), rapid.IntRange(0, n+1)).Draw(t, label)
}

func (tr scopeTree) rangeArg(t *rapid.T, cwd string) scopeArg {
	f, arg, inside := tr.pickFile(t, cwd)
	s, e := drawLine(t, f.lines, "start"), drawLine(t, f.lines, "end")
	if rapid.IntRange(0, 3).Draw(t, "inverted by one") == 0 {
		e = s - 1
	}
	a := scopeArg{arg: fmt.Sprintf("%s:%d-%d", arg, s, e)}
	if !inside || !f.eligible() || s < 1 || s > e || e > f.lines {
		a.bad = true
		return a
	}
	lines := map[int]bool{}
	for l := s; l <= e; l++ {
		lines[l] = true
	}
	a.hits = map[string]scopeHit{f.rel: {lines: lines}}
	return a
}

func (tr scopeTree) globArg(t *rapid.T) scopeArg {
	g := rapid.SampledFrom(scopeGlobs).Draw(t, "glob")
	compiled, err := core.CompileGlob(g)
	if err != nil {
		return scopeArg{arg: g, bad: true}
	}
	return tr.wholeArg(g, func(f scopeTreeFile) bool { return compiled.Match(f.rel) })
}

func drawScopeArg(t *rapid.T, tr scopeTree, cwd string) scopeArg {
	switch rapid.IntRange(0, 7).Draw(t, "kind") {
	case 0:
		return tr.fileArg(t, cwd)
	case 1:
		return tr.dirArg(t, cwd)
	case 2, 3, 4:
		return tr.rangeArg(t, cwd)
	case 5:
		_, arg, _ := tr.pickFile(t, cwd)
		return scopeArg{arg: arg + ":" + rapid.SampledFrom(scopeBadRanges).Draw(t, "bad range"), bad: true}
	case 6:
		return tr.globArg(t)
	default:
		return scopeArg{arg: "does/not/exist", bad: true}
	}
}

func drawScopeArgs(t *rapid.T, tr scopeTree, cwd string) []scopeArg {
	gen := rapid.Custom(func(t *rapid.T) scopeArg { return drawScopeArg(t, tr, cwd) })
	if !slices.ContainsFunc(tr.files, scopeTreeFile.eligible) {
		return rapid.SliceOfN(gen, 1, 4).Draw(t, "args")
	}
	switch rapid.IntRange(0, 2).Draw(t, "mode") {
	case 0:
		return rapid.SliceOfN(gen, 1, 4).Draw(t, "args")
	case 1:
		return rapid.SliceOfN(gen.Filter(func(a scopeArg) bool { return !a.bad }), 1, 4).Draw(t, "valid args")
	default:
		args := rapid.SliceOfN(gen.Filter(func(a scopeArg) bool { return !a.bad }), 0, 3).Draw(t, "valid args")
		bad := gen.Filter(func(a scopeArg) bool { return a.bad }).Draw(t, "the one bad arg")
		i := rapid.IntRange(0, len(args)).Draw(t, "bad arg position")
		return slices.Insert(args, i, bad)
	}
}

func coveredLines(t *rapid.T, sf ScopeFile, n int) map[int]bool {
	got := map[int]bool{}
	if len(sf.Ranges) == 0 {
		for l := 1; l <= n; l++ {
			got[l] = true
		}
		return got
	}
	for _, r := range sf.Ranges {
		if r[0] < 1 || r[0] > r[1] || r[1] > n {
			t.Fatalf("%s: range %v is outside 1..%d or inverted", sf.Path, r, n)
		}
		for l := r[0]; l <= r[1]; l++ {
			got[l] = true
		}
	}
	return got
}

func mergeHits(args []scopeArg) map[string]scopeHit {
	want := map[string]scopeHit{}
	for _, a := range args {
		for p, h := range a.hits {
			w := want[p]
			w.whole = w.whole || h.whole
			if w.lines == nil {
				w.lines = map[int]bool{}
			}
			for l := range h.lines {
				w.lines[l] = true
			}
			want[p] = w
		}
	}
	return want
}

func checkScopeFile(t *rapid.T, tr scopeTree, sf ScopeFile, w scopeHit) {
	f, _ := tr.byRel(sf.Path)
	lang, role := scopeClaim(sf.Path)
	if sf.Lang != lang || sf.Role != role {
		t.Fatalf("%s: lang %q role %q, want %q %q from the adapter's classification", sf.Path, sf.Lang, sf.Role, lang, role)
	}
	if w.whole && len(sf.Ranges) != 0 {
		t.Fatalf("%s: ranges %v, but a whole-file argument selects it and must absorb ranges", sf.Path, sf.Ranges)
	}
	cov := coveredLines(t, sf, f.lines)
	exp := w.lines
	if w.whole {
		exp = map[int]bool{}
		for l := 1; l <= f.lines; l++ {
			exp[l] = true
		}
	}
	if !mapsEqualBool(cov, exp) {
		t.Fatalf("%s: covers lines %v, want %v", sf.Path, slices.Sorted(mapKeys(cov)), slices.Sorted(mapKeys(exp)))
	}
}

func checkScopeResult(t *rapid.T, tr scopeTree, args []scopeArg, got []ScopeFile) {
	want := mergeHits(args)
	seen := map[string]bool{}
	for _, sf := range got {
		if seen[sf.Path] {
			t.Fatalf("%s listed twice; several arguments hitting one file must merge into one entry", sf.Path)
		}
		seen[sf.Path] = true
		w, ok := want[sf.Path]
		if !ok {
			t.Fatalf("resolved %s, which no argument selects as a source or test file (want %v)", sf.Path, slices.Sorted(mapKeys(want)))
		}
		checkScopeFile(t, tr, sf, w)
	}
	for p := range want {
		if !seen[p] {
			t.Fatalf("%s selected by an argument but missing from the resolved scope %+v", p, got)
		}
	}
}

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func mapsEqualBool(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func TestResolveScopeSelectsExactlyTheNamedSourceAndTestLines(t *testing.T) {
	isolateGit(t)
	bin := scopeAdapters(t, "xx", "yy")
	defer func() { _ = os.RemoveAll(bin) }()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	rapid.Check(t, func(rt *rapid.T) {
		dir, err := os.MkdirTemp("", "assure-scope")
		if err != nil {
			rt.Fatal(err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		gitRoot, err := filepath.EvalSymlinks(dir)
		if err != nil {
			rt.Fatal(err)
		}
		sub := rapid.SampledFrom([]string{"", "mod/"}).Draw(rt, "manifest dir")
		tr := buildScopeTree(rt, gitRoot, sub, drawScopeFiles(rt))
		m, err := ManifestFor(tr.root)
		if err != nil {
			rt.Fatal(err)
		}
		cwd := filepath.Join(tr.root, filepath.FromSlash(rapid.SampledFrom(tr.dirs).Draw(rt, "cwd")))
		args := drawScopeArgs(rt, tr, cwd)
		strs := make([]string, len(args))
		var bad []string
		for i, a := range args {
			strs[i] = a.arg
			if a.bad {
				bad = append(bad, a.arg)
			}
		}
		got, err := ResolveScope(m, cwd, strs)
		if len(bad) > 0 {
			checkUsageError(rt, strs, bad, got, err)
			return
		}
		if err != nil {
			rt.Fatalf("args %q from cwd %s: %v", strs, cwd, err)
		}
		checkScopeResult(rt, tr, args, got)
	})
}

func drawScopeFiles(t *rapid.T) []scopeTreeFile {
	rels := rapid.SliceOfNDistinct(rapid.SampledFrom(scopeTreePaths), 1, 6, rapid.ID[string]).Draw(t, "paths")
	files := make([]scopeTreeFile, len(rels))
	for i, rel := range rels {
		files[i] = scopeTreeFile{
			rel:       rel,
			lines:     rapid.IntRange(1, 6).Draw(t, "lines "+rel),
			trailing:  rapid.Bool().Draw(t, "trailing newline "+rel),
			committed: rapid.Bool().Draw(t, "committed "+rel),
			ignored:   rapid.Bool().Draw(t, "ignored "+rel),
		}
	}
	return files
}

func checkUsageError(t *rapid.T, args, bad []string, got []ScopeFile, err error) {
	ue, ok := errors.AsType[*UsageError](err)
	if !ok {
		t.Fatalf("args %q: got %+v, err %v; want a *UsageError naming one of %q", args, got, err, bad)
	}
	if !slices.Contains(bad, ue.Arg) || !strings.Contains(ue.Error(), ue.Arg) {
		t.Fatalf("args %q: usage error names %q (%q); want one of %q, named in the message", args, ue.Arg, ue.Error(), bad)
	}
}

func TestResolveScopeRangesAreBoundedByTheFileAndMergePerFile(t *testing.T) {
	isolateGit(t)
	bin := scopeAdapters(t, "xx", "yy")
	defer func() { _ = os.RemoveAll(bin) }()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var files []scopeTreeFile
	for n := 1; n <= 4; n++ {
		files = append(files,
			scopeTreeFile{rel: fmt.Sprintf("a/m%d.xx", n), lines: n, trailing: true, committed: true},
			scopeTreeFile{rel: fmt.Sprintf("a/m%d_test.yy", n), lines: n},
			scopeTreeFile{rel: fmt.Sprintf("c/m%d.gen.xx", n), lines: n, trailing: true, committed: true},
		)
	}
	tr := buildScopeTree(t, root, "", files)
	m, err := ManifestFor(root)
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(rt *rapid.T) {
		f := rapid.SampledFrom(files).Draw(rt, "file")
		args := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) scopeArg {
			if rapid.IntRange(0, 3).Draw(t, "whole") == 0 {
				return tr.wholeArg(f.rel, func(g scopeTreeFile) bool { return g.rel == f.rel })
			}
			s, e := drawLine(t, f.lines, "start"), drawLine(t, f.lines, "end")
			if rapid.IntRange(0, 3).Draw(t, "inverted by one") == 0 {
				e = s - 1
			}
			a := scopeArg{arg: fmt.Sprintf("%s:%d-%d", f.rel, s, e), bad: !f.eligible() || s < 1 || s > e || e > f.lines}
			if !a.bad {
				a.hits = map[string]scopeHit{f.rel: {lines: map[int]bool{}}}
				for l := s; l <= e; l++ {
					a.hits[f.rel].lines[l] = true
				}
			}
			return a
		}), 1, 3).Draw(rt, "args")
		strs := make([]string, len(args))
		var bad []string
		for i, a := range args {
			strs[i] = a.arg
			if a.bad {
				bad = append(bad, a.arg)
			}
		}
		got, err := ResolveScope(m, root, strs)
		if len(bad) > 0 {
			checkUsageError(rt, strs, bad, got, err)
			return
		}
		if err != nil {
			rt.Fatalf("args %q: %v", strs, err)
		}
		checkScopeResult(rt, tr, args, got)
	})
}

func TestResolveScopeMissingAdapterIsNotAUsageError(t *testing.T) {
	isolateGit(t)
	bin := scopeAdapters(t, "xx")
	defer func() { _ = os.RemoveAll(bin) }()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	buildScopeTree(t, root, "", []scopeTreeFile{{rel: "a/m.xx", lines: 3, trailing: true, committed: true}})
	m, err := ManifestFor(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveScope(m, root, []string{"a/m.xx"})
	if err == nil {
		t.Fatalf("resolved %+v with assure-adapter-yy missing from PATH; classification of yy files is unknown, so resolution must fail", got)
	}
	if _, ok := errors.AsType[*UsageError](err); ok {
		t.Fatalf("missing adapter reported as a usage error (%v); the arguments are fine, the environment is not, so the command must exit 1, not 2", err)
	}
	if !strings.Contains(err.Error(), "assure-adapter-yy") {
		t.Fatalf("error %q does not name the missing adapter", err)
	}
}

func FuzzResolveScopeArgument(f *testing.F) {
	f.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	f.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	bin := scopeAdapters(f, "xx", "yy")
	f.Cleanup(func() { _ = os.RemoveAll(bin) })
	f.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root, err := filepath.EvalSymlinks(f.TempDir())
	if err != nil {
		f.Fatal(err)
	}
	tr := buildScopeTree(f, root, "", []scopeTreeFile{
		{rel: "a/m.xx", lines: 5, trailing: true, committed: true},
		{rel: "a/m_test.xx", lines: 2, trailing: true, committed: true},
		{rel: "c/n.gen.xx", lines: 3, trailing: true, committed: true},
		{rel: "c/n.yy", lines: 4, committed: true},
		{rel: "a/b/n.md", lines: 1, trailing: true},
		{rel: "a/b/m.xx", lines: 2, trailing: true, ignored: true},
	})
	m, err := ManifestFor(root)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"a", ".", "a/m.xx:2-4", "a/m.xx:4-2", "a/m.xx:0-1", "a/m.xx:1-99", "c/n.yy:4-4", "c/n.yy:5-5",
		"**/*.xx", "**", "[", "does/not/exist", "../x", "/", "", ":", "a/m.xx:1-2:3-4", "c/n.gen.xx", "a/b", "a/b/m.xx", "a\x00b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, arg string) {
		got, err := ResolveScope(m, root, []string{arg})
		if err != nil {
			ue, ok := errors.AsType[*UsageError](err)
			if !ok || ue.Arg != arg {
				t.Fatalf("arg %q: error %v; want a *UsageError whose Arg is the argument", arg, err)
			}
			return
		}
		checkFuzzScope(t, tr, arg, got)
	})
}

func checkFuzzScope(t *testing.T, tr scopeTree, arg string, got []ScopeFile) {
	if len(got) == 0 {
		t.Fatalf("arg %q resolved to nothing without a usage error; every argument must match a source or test file", arg)
	}
	seen := map[string]bool{}
	for _, sf := range got {
		fl, ok := tr.byRel(sf.Path)
		if !ok || !fl.eligible() || seen[sf.Path] {
			t.Fatalf("arg %q resolved %+v: not a distinct candidate source or test file", arg, sf)
		}
		seen[sf.Path] = true
		for _, r := range sf.Ranges {
			if r[0] < 1 || r[0] > r[1] || r[1] > fl.lines {
				t.Fatalf("arg %q: %s range %v outside 1..%d or inverted", arg, sf.Path, r, fl.lines)
			}
		}
	}
}

func TestResolveScopeRangeWithSignOrSpaceIsUsageError(t *testing.T) {
	isolateGit(t)
	bin := scopeAdapters(t, "xx")
	defer func() { _ = os.RemoveAll(bin) }()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	buildScopeTree(t, root, "", []scopeTreeFile{{rel: "a/m.xx", lines: 9, trailing: true, committed: true}})
	m, err := ManifestFor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"a/m.xx:-1-5", "a/m.xx:+1-5", "a/m.xx: 1-5", "a/m.xx:1 -5", "a/m.xx:1-+5", "a/m.xx:1-5 "} {
		got, err := ResolveScope(m, root, []string{arg})
		if ue, ok := errors.AsType[*UsageError](err); !ok || ue.Arg != arg {
			t.Errorf("%q: got %+v, err %v; strconv.Atoi accepts signs, but a range is decimal digits only, so a typo must be rejected rather than read as another range", arg, got, err)
		}
	}
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.IntRange(1, 9).Draw(rt, "start")
		e := rapid.IntRange(s, 9).Draw(rt, "end")
		spec := fmt.Sprintf("%d-%d", s, e)
		i := rapid.IntRange(0, len(spec)).Draw(rt, "insert at")
		c := rapid.SampledFrom([]string{"+", "-", " ", "\t", "\n"}).Draw(rt, "char")
		arg := "a/m.xx:" + spec[:i] + c + spec[i:]
		got, err := ResolveScope(m, root, []string{arg})
		if ue, ok := errors.AsType[*UsageError](err); !ok || ue.Arg != arg {
			rt.Fatalf("%q: got %+v, err %v; want a usage error naming it", arg, got, err)
		}
	})
}

func TestResolveScopeAbsolutePathEqualsRelativeForm(t *testing.T) {
	isolateGit(t)
	bin := scopeAdapters(t, "xx", "yy")
	defer func() { _ = os.RemoveAll(bin) }()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	gitRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := buildScopeTree(t, gitRoot, "mod/", []scopeTreeFile{
		{rel: "m.xx", lines: 3, trailing: true, committed: true},
		{rel: "a/m_test.yy", lines: 4, committed: true},
		{rel: "a/b/n.xx", lines: 2, trailing: true},
		{rel: "c/n.gen.xx", lines: 2, trailing: true, committed: true},
	})
	m, err := ManifestFor(tr.root)
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(rt *rapid.T) {
		cwd := filepath.Join(tr.root, filepath.FromSlash(rapid.SampledFrom(tr.dirs).Draw(rt, "cwd")))
		var abs string
		if rapid.Bool().Draw(rt, "dir") {
			abs = filepath.Join(tr.root, filepath.FromSlash(rapid.SampledFrom(tr.dirs).Draw(rt, "target dir")))
		} else {
			abs = filepath.Join(tr.root, filepath.FromSlash(rapid.SampledFrom(tr.files).Draw(rt, "target file").rel))
			if rapid.Bool().Draw(rt, "range") {
				abs += fmt.Sprintf(":%d-%d", rapid.IntRange(0, 3).Draw(rt, "s"), rapid.IntRange(0, 4).Draw(rt, "e"))
			}
		}
		rel := tr.relFromCwd(rt, cwd, abs)
		if strings.Contains(abs, ":") {
			path, rng, _ := strings.Cut(abs, ":")
			rel = tr.relFromCwd(rt, cwd, path) + ":" + rng
		}
		gotRel, errRel := ResolveScope(m, cwd, []string{rel})
		gotAbs, errAbs := ResolveScope(m, cwd, []string{abs})
		_, uRel := errors.AsType[*UsageError](errRel)
		_, uAbs := errors.AsType[*UsageError](errAbs)
		if (errRel == nil) != (errAbs == nil) || uRel != uAbs || !reflect.DeepEqual(gotRel, gotAbs) {
			rt.Fatalf("relative %q -> %+v, %v; absolute %q -> %+v, %v; both name the same path, so they must resolve alike", rel, gotRel, errRel, abs, gotAbs, errAbs)
		}
	})
	for _, arg := range []string{filepath.Join(gitRoot, "outside.xx"), filepath.Join(gitRoot, "outside.xx") + ":1-2", gitRoot} {
		got, err := ResolveScope(m, tr.root, []string{arg})
		ue, ok := errors.AsType[*UsageError](err)
		if !ok || ue.Arg != arg || !strings.HasPrefix(ue.Reason, "is outside the manifest root") {
			t.Errorf("%q: got %+v, err %v; a tracked file above the manifest root has no level, so it must be a usage error", arg, got, err)
		}
	}
}
