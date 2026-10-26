package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func gitQuiet(t fataler, dir string, args ...string) string {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"--no-optional-locks"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// repoState is everything building a tree must leave alone: status, the
// worktree list, the index bytes, and every file's mode and bytes outside
// .git. --no-optional-locks keeps the snapshot itself from refreshing the
// index.
func repoState(t fataler, top string) string {
	var b strings.Builder
	index, err := os.ReadFile(filepath.Join(top, ".git", "index"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	fmt.Fprintf(&b, "index %x\n", sha256.Sum256(index))
	b.WriteString(gitQuiet(t, top, "status", "--porcelain", "--ignored"))
	b.WriteString(gitQuiet(t, top, "worktree", "list", "--porcelain"))
	err = filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %v", p, info.Mode())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, " %x", sha256.Sum256(data))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return b.String()
}

// treeFiles reads a built tree: regular files by top-relative path, and
// symlinks as "-> target".
func treeFiles(t fataler, dst string) map[string]string {
	out := map[string]string{}
	err := filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := relSlash(dst, p)
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			out[rel] = "-> " + target
			return err
		}
		data, err := os.ReadFile(p)
		out[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return out
}

type fileState int

const (
	absent fileState = iota
	tracked
	trackedHeadEdit
	trackedWorkEdit
	trackedDeleted
	untracked
)

var fileStateGen = rapid.SampledFrom([]fileState{absent, tracked, tracked, trackedHeadEdit, trackedWorkEdit, trackedDeleted, untracked})

const shadowIgnore = "*.log\nignored/\n"

func shadowIgnored(p string) bool {
	return strings.HasSuffix(p, ".log") || slices.Contains(strings.Split(path.Dir(p), "/"), "ignored")
}

// shadowPool is every path a generated repository may hold for a unit at
// unitRel: top-level and ancestor files, siblings of the unit and of its
// ancestors, and the unit's own nested, ignored and nested-unit files.
func shadowPool(unitRel string) []string {
	pool := []string{"README.md", "conftest.py", "pyproject.toml", "top.log", "sib/s.py", "sib/ignored/z.py", "a/conftest.py", "a/pyproject.toml", "a/sib/t.py", "a/b/conftest.py"}
	for _, p := range []string{"app/mod.py", "tests/test_m.py", "deep/er/f.txt", "ignored/z.py", "y.log", "sub/pyproject.toml", "sub/x.py"} {
		pool = append(pool, path.Join(unitRel, p))
	}
	slices.Sort(pool)
	return slices.Compact(pool)
}

func shadowAncestors(unitRel string) []string {
	var out []string
	for d := unitRel; d != "."; {
		d = path.Dir(d)
		out = append(out, d)
	}
	return out
}

func inShadowTree(unitRel, p string) bool {
	return unitRel == "." || strings.HasPrefix(p, unitRel+"/") || slices.Contains(shadowAncestors(unitRel), path.Dir(p))
}

type shadowRepo struct {
	top, unitRel, base string
	wantBase, wantWork map[string]string
}

// drawShadowRepo commits a base, then a head commit, then leaves working
// changes, recording which bytes each tree must hold.
func drawShadowRepo(t *rapid.T, top string) shadowRepo {
	r := shadowRepo{top: top, unitRel: rapid.SampledFrom([]string{".", "a", "a/b"}).Draw(t, "unit"), wantBase: map[string]string{}, wantWork: map[string]string{}}
	states := map[string]fileState{}
	for _, p := range shadowPool(r.unitRel) {
		states[p] = fileStateGen.Draw(t, p)
	}
	writeTree(t, top, map[string]string{".gitignore": shadowIgnore})
	git(t, top, "init", "-q")
	add := []string{"add", "-f", "--", ".gitignore"}
	for p, s := range states {
		if s != absent && s != untracked {
			writeTree(t, top, map[string]string{p: "base:" + p})
			add = append(add, p)
		}
	}
	git(t, top, add...)
	git(t, top, "commit", "-q", "-m", "base")
	r.base = strings.TrimSpace(gitQuiet(t, top, "rev-parse", "HEAD"))
	for p, s := range states {
		if s == trackedHeadEdit {
			writeTree(t, top, map[string]string{p: "head:" + p})
		}
	}
	git(t, top, "commit", "-q", "-a", "--allow-empty", "-m", "head")
	r.wantBase[".gitignore"], r.wantWork[".gitignore"] = shadowIgnore, shadowIgnore
	for p, s := range states {
		switch s {
		case trackedWorkEdit:
			writeTree(t, top, map[string]string{p: "work:" + p})
		case trackedDeleted:
			if err := os.Remove(filepath.Join(top, filepath.FromSlash(p))); err != nil {
				t.Fatalf("%v", err)
			}
		case untracked:
			writeTree(t, top, map[string]string{p: "new:" + p})
		}
		if inShadowTree(r.unitRel, p) {
			r.expect(p, s)
		}
	}
	return r
}

func (r shadowRepo) expect(p string, s fileState) {
	if s != absent && s != untracked {
		r.wantBase[p] = "base:" + p
	}
	switch {
	case s == tracked:
		r.wantWork[p] = "base:" + p
	case s == trackedHeadEdit:
		r.wantWork[p] = "head:" + p
	case s == trackedWorkEdit:
		r.wantWork[p] = "work:" + p
	case s == untracked && !shadowIgnored(p):
		r.wantWork[p] = "new:" + p
	}
}

func diffTrees(got, want map[string]string) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if g, ok := got[k]; !ok {
			fmt.Fprintf(&b, "  missing %s\n", k)
		} else if g != want[k] {
			fmt.Fprintf(&b, "  %s = %q, want %q\n", k, g, want[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[k]; !ok {
			fmt.Fprintf(&b, "  extra %s\n", k)
		}
	}
	return b.String()
}

// TestShadowTreesMirrorRepository is task 6.1. Fail-on-base runs tests in
// the base tree and mutation in the working copy, so a file from HEAD in the
// base tree hides a regression, a missing parent conftest.py breaks
// collection, and an ignored or sibling file leaks code the unit does not
// own. Building either tree must leave the repository exactly as it was
// ("Repository untouched").
func TestShadowTreesMirrorRepository(t *testing.T) {
	isolateGit(t)
	base := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		r := drawShadowRepo(t, top)
		unit := filepath.Join(top, filepath.FromSlash(r.unitRel))
		before := repoState(t, top)
		baseDst, workDst := filepath.Join(top+"-base"), filepath.Join(top+"-work")
		if err := baseTree(top, r.base, unit, baseDst); err != nil {
			t.Fatalf("baseTree: %v", err)
		}
		if err := workTree(top, unit, workDst); err != nil {
			t.Fatalf("workTree: %v", err)
		}
		if after := repoState(t, top); after != before {
			t.Fatalf("building the trees changed the repository\nbefore:\n%s\nafter:\n%s\nspec \"Repository untouched\": status, worktree list, index and files must be identical", before, after)
		}
		if d := diffTrees(treeFiles(t, baseDst), r.wantBase); d != "" {
			t.Fatalf("base tree for unit %s differs:\n%s\nit holds the unit's files and each ancestor directory's own files as committed at --changed-from, nothing from HEAD, the worktree, or sibling directories", r.unitRel, d)
		}
		if d := diffTrees(treeFiles(t, workDst), r.wantWork); d != "" {
			t.Fatalf("working tree for unit %s differs:\n%s\nit holds the current bytes of the unit's tracked and untracked-not-ignored files and each ancestor directory's own such files; deleted, ignored and sibling files stay out", r.unitRel, d)
		}
	})
}

func shadowSpecialRepo(t *testing.T) (top, unit, base string) {
	isolateGit(t)
	top = realTempDir(t)
	writeTree(t, top, map[string]string{"conftest.py": "", "svc/app/mod.py": "x = 1\n", "svc/run.sh": "#!/bin/sh\n", "tool.sh": "#!/bin/sh\n"})
	for _, p := range []string{"svc/run.sh", "tool.sh"} {
		if err := os.Chmod(filepath.Join(top, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"svc/alias.py": "app/mod.py", "svc/app/dangling.py": "missing.py", "shared.py": "conftest.py"} {
		if err := os.Symlink(target, filepath.Join(top, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	return top, filepath.Join(top, "svc"), strings.TrimSpace(gitQuiet(t, top, "rev-parse", "HEAD"))
}

func checkSpecial(t *testing.T, which, dst string) {
	t.Helper()
	files := treeFiles(t, dst)
	for link, target := range map[string]string{"svc/alias.py": "app/mod.py", "shared.py": "conftest.py"} {
		if got := files[link]; got != "-> "+target {
			t.Errorf("%s tree: %s = %q, want a symlink to %s; a committed symlink (a shared conftest.py, a vendored package) is part of the code under test and must keep pointing inside the tree", which, link, got, target)
		}
	}
	for _, p := range []string{"svc/run.sh", "tool.sh"} {
		info, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(p)))
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s tree: %s mode %v (%v); an executable file a test runs as a subprocess must stay executable", which, p, info.Mode(), err)
		}
	}
}

func TestWorkTreeKeepsSymlinksAndModes(t *testing.T) {
	top, unit, _ := shadowSpecialRepo(t)
	dst := filepath.Join(t.TempDir(), "w")
	if err := workTree(top, unit, dst); err != nil {
		t.Fatal(err)
	}
	checkSpecial(t, "working", dst)
}

func TestBaseTreeKeepsSymlinksAndModes(t *testing.T) {
	top, unit, base := shadowSpecialRepo(t)
	dst := filepath.Join(t.TempDir(), "b")
	if err := baseTree(top, base, unit, dst); err != nil {
		t.Fatal(err)
	}
	checkSpecial(t, "base", dst)
}

func TestBaseTreeUnknownRefFails(t *testing.T) {
	top, unit, _ := shadowSpecialRepo(t)
	if err := baseTree(top, "no-such-ref", unit, t.TempDir()); err == nil {
		t.Fatal("baseTree at an unknown ref succeeded; fail-on-base would run against an empty tree and every new test would fail as if it caught a regression")
	}
}

// TestOverlayTestsMatchesModel: fail-on-base runs the changed test files'
// current content against base code, so every changed test file must be the
// current bytes, a deleted one gone, and everything else base.
func TestOverlayTestsMatchesModel(t *testing.T) {
	base := realTempDir(t)
	names := []string{"tests/test_a.py", "tests/test_b.py", "tests/new/test_c.py", "test_d.py"}
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(base, "ov")
		if err != nil {
			t.Fatalf("%v", err)
		}
		root, tree := filepath.Join(dir, "root"), filepath.Join(dir, "tree")
		want := map[string]string{"app/mod.py": "base app"}
		writeTree(t, tree, want)
		var changed []string
		for _, n := range names {
			inBase, inRoot := rapid.Bool().Draw(t, "base:"+n), rapid.Bool().Draw(t, "root:"+n)
			if inBase {
				writeTree(t, tree, map[string]string{n: "base " + n})
				want[n] = "base " + n
			}
			if inRoot {
				writeTree(t, root, map[string]string{n: "current " + n})
			}
			if rapid.Bool().Draw(t, "changed:"+n) {
				changed = append(changed, n)
				if inRoot {
					want[n] = "current " + n
				} else {
					delete(want, n)
				}
			}
		}
		if err := overlayTests(root, tree, changed); err != nil {
			t.Fatalf("overlayTests: %v", err)
		}
		if d := diffTrees(treeFiles(t, tree), want); d != "" {
			t.Fatalf("after overlaying %q:\n%s", changed, d)
		}
	})
}

func TestTreeEnv(t *testing.T) {
	sep := string(os.PathListSeparator)
	base := t.TempDir()
	t.Setenv("PYTHONPATH", "")
	rapid.Check(t, func(t *rapid.T) {
		unit := filepath.Join(tempIn(t, base), "unit")
		writeTree(t, unit, map[string]string{"x.py": ""})
		want := unit
		if rapid.Bool().Draw(t, "src") {
			writeTree(t, unit, map[string]string{"src/demo/__init__.py": ""})
			want += sep + filepath.Join(unit, "src")
		}
		old := rapid.SampledFrom([]string{"", "/opt/a", "/opt/a" + sep + "/opt/b"}).Draw(t, "old")
		if err := os.Setenv("PYTHONPATH", old); err != nil {
			t.Fatalf("%v", err)
		}
		if old != "" {
			want += sep + old
		}
		if got := treeEnv(unit); !slices.Equal(got, []string{"PYTHONPATH=" + want}) {
			t.Fatalf("treeEnv = %q, want PYTHONPATH=%s; the tree's unit and its src/ go first so they precede the editable install's paths, and the adopter's PYTHONPATH is kept after them", got, want)
		}
	})
}

func tempIn(t *rapid.T, base string) string {
	d, err := os.MkdirTemp(base, "u")
	if err != nil {
		t.Fatalf("%v", err)
	}
	return d
}

var (
	importableNames = []string{"demo", "_priv", "pkg2", "tests", "café", "π_2", "x٣"}
	pkgNameGen      = rapid.SampledFrom(append([]string{"not-ident", "1abc", "٣x", "has.dot", "with space", "a€"}, importableNames...))
)

// TestTopLevelPackagesMatchesModel: the probe checks exactly the packages
// the tree's tests would import first: identifier-named directories with
// __init__.py directly in the unit or its src/.
func TestTopLevelPackagesMatchesModel(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		unit := tempIn(t, base)
		var want []string
		useSrc := rapid.Bool().Draw(t, "src")
		roots := []string{"."}
		if useSrc {
			roots = append(roots, "src")
			writeTree(t, unit, map[string]string{"src/.keep": ""})
		}
		for range rapid.IntRange(0, 5).Draw(t, "entries") {
			root := rapid.SampledFrom(roots).Draw(t, "root")
			name := pkgNameGen.Draw(t, "name")
			switch rapid.SampledFrom([]string{"package", "namespace", "nested", "file"}).Draw(t, "kind") {
			case "package":
				writeTree(t, unit, map[string]string{path.Join(root, name, "__init__.py"): ""})
				if slices.Contains(importableNames, name) {
					want = append(want, name)
				}
			case "namespace":
				writeTree(t, unit, map[string]string{path.Join(root, name, "mod.py"): ""})
			case "nested":
				writeTree(t, unit, map[string]string{path.Join(root, "outer", name, "__init__.py"): ""})
			case "file":
				writeTree(t, unit, map[string]string{path.Join(root, name+".py"): ""})
			}
		}
		slices.Sort(want)
		want = slices.Compact(want)
		got, err := topLevelPackages(unit)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("topLevelPackages = %q, want %q", got, want)
		}
	})
}

// leakFinder is a sitecustomize that installs a meta-path finder at the
// front of sys.meta_path mapping demo to a directory outside the tree, as
// some editable installs do. PathFinder never sees the PYTHONPATH prepend.
const leakFinder = `import sys, importlib.util
class _Leak:
    @classmethod
    def find_spec(cls, name, path=None, target=None):
        if name == "demo":
            return importlib.util.spec_from_file_location("demo", %q, submodule_search_locations=[%q])
        return None
sys.meta_path.insert(0, _Leak)
`

func probeFixture(t *testing.T) (treeRoot, treeUnit, outside string) {
	unreal := t.TempDir()
	treeRoot = filepath.Join(unreal, "tree")
	treeUnit = filepath.Join(treeRoot, "svc")
	outside = filepath.Join(unreal, "repo", "src", "demo")
	writeTree(t, treeUnit, map[string]string{"src/demo/__init__.py": "", "not-ident/__init__.py": "", "ghost/mod.py": ""})
	writeTree(t, outside, map[string]string{"__init__.py": ""})
	return treeRoot, treeUnit, outside
}

func TestProbeAcceptsTreeAheadOfPythonPath(t *testing.T) {
	py := testPython(t)
	treeRoot, treeUnit, outside := probeFixture(t)
	t.Setenv("PYTHONPATH", filepath.Dir(outside))
	msg, err := probeTree(py, filepath.Dir(treeRoot), treeRoot, treeUnit)
	if err != nil || msg != "" {
		t.Fatalf("probeTree = %q, %v; demo resolves to the tree because treeEnv puts it ahead of the adopter's PYTHONPATH (here holding another demo), and the tree path goes through a symlinked temp dir, which realpath comparison must accept", msg, err)
	}
}

func TestProbeReportsMetaPathLeak(t *testing.T) {
	py := testPython(t)
	treeRoot, treeUnit, outside := probeFixture(t)
	custom := t.TempDir()
	writeTree(t, custom, map[string]string{"sitecustomize.py": fmt.Sprintf(leakFinder, filepath.Join(outside, "__init__.py"), outside)})
	t.Setenv("PYTHONPATH", custom)
	msg, err := probeTree(py, filepath.Dir(treeRoot), treeRoot, treeUnit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "demo") || !strings.Contains(msg, outside) {
		t.Fatalf("probeTree = %q; a finder ahead of PathFinder imports demo from %s, so tests would run the repository's code, and the spec's leak must fail closed naming the package and where it comes from", msg, outside)
	}
	if strings.Contains(msg, "ghost") || strings.Contains(msg, "not-ident") {
		t.Fatalf("probeTree = %q; only identifier-named directories with __init__.py are packages to probe", msg)
	}
}

func TestProbeFailuresFailClosed(t *testing.T) {
	treeRoot, treeUnit, _ := probeFixture(t)
	for name, script := range map[string]string{"crash": "echo boom >&2; exit 3", "garbage": "echo not json"} {
		py := fakePython(t, script)
		if msg, err := probeTree(py, filepath.Dir(treeRoot), treeRoot, treeUnit); err == nil {
			t.Fatalf("%s: probeTree = %q, nil; a probe that cannot answer must not count as no leak", name, msg)
		}
	}
	empty := t.TempDir()
	if msg, err := probeTree(filepath.Join(empty, "no-python"), empty, empty, empty); err != nil || msg != "" {
		t.Fatalf("probeTree with no packages = %q, %v; nothing to probe needs no interpreter", msg, err)
	}
}
