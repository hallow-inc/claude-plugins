package main

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func realTempDir(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func isolateGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func git(t fataler, dir string, args ...string) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{
		"-c", "commit.gpgsign=false", "-c", "core.excludesFile=/dev/null",
		"-c", "user.name=assure", "-c", "user.email=assure@example.invalid",
	}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeTree(t fataler, root string, files map[string]string) {
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("%v", err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("%v", err)
		}
	}
}

var dirSegGen = rapid.SampledFrom([]string{"a", "b", "c"})

func TestUnitIsNearestPackageJSONUpToTop(t *testing.T) {
	base := realTempDir(t)
	writeTree(t, base, map[string]string{"package.json": "{}"})
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		chain := rapid.SliceOfN(dirSegGen, 0, 4).Draw(t, "chain")
		want, wantOK := "", false
		for i := 0; i <= len(chain); i++ {
			if rapid.Bool().Draw(t, "pkg") {
				dir := filepath.Join(append([]string{top}, chain[:i]...)...)
				writeTree(t, dir, map[string]string{"package.json": "{}"})
				want, wantOK = dir, true
			}
		}
		if rapid.Bool().Draw(t, "deeperPkg") {
			writeTree(t, top, map[string]string{strings.Join(append(slices.Clone(chain), "z", "package.json"), "/"): "{}"})
		}
		start := filepath.Join(append([]string{top}, chain...)...)
		got, ok := unitOf(top, start)
		if got != want || ok != wantOK {
			t.Fatalf("unitOf(%s) = %q,%v; want %q,%v; the unit decides which tests and lint config apply, and a package.json above the git top-level or below the file must not count", chain, got, ok, want, wantOK)
		}
	})
}

func drawPackageDirs(t *rapid.T, top string) map[string]bool {
	pkgDirs := map[string]bool{}
	for _, d := range rapid.SliceOfN(rapid.SliceOfN(dirSegGen, 0, 2), 0, 4).Draw(t, "pkgDirs") {
		rel := strings.Join(d, "/")
		pkgDirs[rel] = true
		writeTree(t, top, map[string]string{strings.TrimPrefix(rel+"/package.json", "/"): "{}"})
	}
	return pkgDirs
}

var nonConfigPathGen = rapid.Custom(func(t *rapid.T) string {
	dirs := rapid.SliceOfN(dirSegGen, 0, 3).Draw(t, "dirs")
	return strings.Join(append(dirs, nameGen.Draw(t, "name")), "/")
}).Filter(func(p string) bool { r, _ := specRole(p); return r != "config" })

func drawChanged(t *rapid.T, top string) ([]string, map[string]bool) {
	changed := rapid.SliceOfN(nonConfigPathGen, 1, 5).Draw(t, "changed")
	present := map[string]bool{}
	for _, p := range changed {
		if rapid.Bool().Draw(t, "exists") {
			writeTree(t, top, map[string]string{p: "export {};\n"})
			present[p] = true
		}
	}
	return changed, present
}

func nearestPackageDir(pkgDirs map[string]bool, p string) (string, bool) {
	for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
		if pkgDirs[strings.TrimPrefix(dir, ".")] {
			return dir, true
		}
		if dir == "." {
			return "", false
		}
	}
}

func expectedSelection(top string, pkgDirs, present map[string]bool, changed []string) (map[string][]string, []string) {
	want := map[string][]string{}
	var orphans []string
	for _, p := range changed {
		role, ok := specRole(p)
		if !ok {
			continue
		}
		dir, ok := nearestPackageDir(pkgDirs, p)
		if !ok {
			orphans = append(orphans, p)
			continue
		}
		unit := filepath.Join(top, dir)
		if want[unit] == nil {
			want[unit] = []string{}
		}
		if (role == "source" || role == "test") && present[p] {
			want[unit] = append(want[unit], p)
		}
	}
	for u := range want {
		slices.Sort(want[u])
		want[u] = slices.Compact(want[u])
	}
	return want, orphans
}

func TestChangedFilesSelectByUnit(t *testing.T) {
	base := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		pkgDirs := drawPackageDirs(t, top)
		changed, present := drawChanged(t, top)
		want, orphans := expectedSelection(top, pkgDirs, present, changed)

		sel, msg, err := selectUnits(top, top, changed)
		if err != nil {
			t.Fatalf("selectUnits(%q): %v", changed, err)
		}
		if len(orphans) > 0 {
			if msg == "" || !slices.ContainsFunc(orphans, func(o string) bool { return strings.Contains(msg, o) }) {
				t.Fatalf("changed %q with no package.json for %q: message %q; run must report environment naming the file instead of skipping it", changed, orphans, msg)
			}
			return
		}
		if msg != "" || !maps.EqualFunc(sel, want, slices.Equal) {
			t.Fatalf("changed %q, package.json in %v:\n got %v %q\nwant %v\nevery unit with a changed claimed file must be run, and only changed source and test files that still exist are selected", changed, slices.Sorted(maps.Keys(pkgDirs)), sel, msg, want)
		}
	})
}

func TestConfigChangeSelectsWholeUnit(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	header := "// Code generated by openapi-gen. DO NOT EDIT.\nexport {};\n"
	writeTree(t, top, map[string]string{
		"package.json":                            "{}",
		"src/root.ts":                             "",
		"packages/api/package.json":               "{}",
		"packages/api/tsconfig.json":              "{}",
		"packages/api/vitest.config.ts":           "",
		"packages/api/src/a.ts":                   "",
		"packages/api/src/a.test.ts":              "",
		"packages/api/src/removed.ts":             "",
		"packages/api/src/gen.ts":                 header,
		"packages/api/src/env.d.ts":               "",
		"packages/api/src/legacy.js":              "",
		"packages/api/deep/er/y.ts":               "",
		"packages/api/plugins/inner/package.json": "{}",
		"packages/api/plugins/inner/x.ts":         "",
		"packages/web/package.json":               "{}",
		"packages/web/b.ts":                       "",
		"packages/web/c.ts":                       "",
	})
	git(t, top, "init", "-q")
	git(t, top, "add", ".")
	git(t, top, "commit", "-q", "-m", "base")
	if err := os.Remove(filepath.Join(top, "packages/api/src/removed.ts")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, top, map[string]string{
		"packages/api/src/new.spec.ts":             "",
		"packages/api/node_modules/dep/index.ts":   "",
		"packages/api/node_modules/dep/index.d.ts": "",
	})
	t.Chdir(top)

	sel, msg, err := selectUnits(top, top, []string{"packages/api/tsconfig.json", "packages/web/b.ts", "packages/api/src/a.ts"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	want := map[string][]string{
		filepath.Join(top, "packages/api"): {"packages/api/deep/er/y.ts", "packages/api/src/a.test.ts", "packages/api/src/a.ts", "packages/api/src/new.spec.ts"},
		filepath.Join(top, "packages/web"): {"packages/web/b.ts"},
	}
	if !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("got %v\nwant %v\na config change must re-check every source and test file of its unit (tracked or untracked), but not a nested unit's files, node_modules, deleted or generated files", sel, want)
	}

	sel, msg, err = selectUnits(top, top, []string{"package.json"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	if want := map[string][]string{top: {"src/root.ts"}}; !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("root package.json change: got %v want %v; the root unit must not absorb the nested packages", sel, want)
	}
}

var layoutGen = rapid.SampledFrom([]string{".pnp.cjs", "deno.json", "deno.jsonc"})

func TestUnsupportedLayoutRefused(t *testing.T) {
	base := realTempDir(t)
	writeTree(t, base, map[string]string{".pnp.cjs": "", "deno.json": ""})
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		chain := rapid.SliceOfN(dirSegGen, 0, 3).Draw(t, "chain")
		unitRel := strings.Join(chain, "/")
		writeTree(t, top, map[string]string{strings.TrimPrefix(unitRel+"/package.json", "/"): "{}"})
		changed := strings.TrimPrefix(unitRel+"/src/a.ts", "/")

		marker := ""
		refused := false
		switch rapid.IntRange(0, 3).Draw(t, "where") {
		case 1:
			marker = strings.Join(append(slices.Clone(chain[:rapid.IntRange(0, len(chain)).Draw(t, "depth")]), layoutGen.Draw(t, "layout")), "/")
			refused = true
		case 2:
			marker = "z/" + layoutGen.Draw(t, "layout")
		case 3:
			marker = strings.TrimPrefix(unitRel+"/src/"+layoutGen.Draw(t, "layout"), "/")
		}
		if marker != "" {
			writeTree(t, top, map[string]string{marker: ""})
		}

		sel, msg, err := selectUnits(top, top, []string{changed})
		if err != nil {
			t.Fatalf("%v", err)
		}
		if refused {
			if !strings.Contains(msg, marker) || !strings.Contains(msg, "unsupported") || len(sel) != 0 {
				t.Fatalf("%s present for unit %q: got %v %q; a Plug'n'Play or Deno layout has no node_modules, so running tools there gives evidence about the wrong install", marker, unitRel, sel, msg)
			}
			return
		}
		if msg != "" || len(sel) != 1 {
			t.Fatalf("marker %q (not the unit or its ancestor below the top-level), unit %q: got %v %q; a file outside the unit's ancestry must not refuse it", marker, unitRel, sel, msg)
		}
	})
}

func TestConfigChangeSelectsOnlyUnderRoot(t *testing.T) {
	isolateGit(t)
	base := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		rootRel := strings.Join(rapid.SliceOfN(rapid.SampledFrom([]string{"app", "svc"}), 1, 2).Draw(t, "root"), "/")
		root := filepath.Join(top, rootRel)
		writeTree(t, top, map[string]string{"package.json": "{}", rootRel + "/tsconfig.json": "{}"})
		files := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string {
			where := rapid.SampledFrom([]string{"", rootRel + "/", "other/", rootRel + "x/", "app/"}).Draw(t, "where")
			return where + nonConfigPathGen.Draw(t, "path")
		}), 1, 6).Draw(t, "files")
		want := []string{}
		for _, f := range files {
			writeTree(t, top, map[string]string{f: "export {};\n"})
			if role, _ := specRole(f); strings.HasPrefix(f, rootRel+"/") && (role == "source" || role == "test") {
				want = append(want, strings.TrimPrefix(f, rootRel+"/"))
			}
		}
		slices.Sort(want)
		want = slices.Compact(want)
		git(t, top, "init", "-q")

		sel, msg, err := selectUnits(root, top, []string{"tsconfig.json"})
		if err != nil || msg != "" {
			t.Fatalf("selectUnits: %v %q", err, msg)
		}
		if w := map[string][]string{top: want}; !maps.EqualFunc(sel, w, slices.Equal) {
			t.Fatalf("root %s under a unit at the git top-level, files %q:\n got %v\nwant %v\nassure evaluates only the manifest root, so a selected path outside it (../...) names a file no objective covers", rootRel, files, sel, w)
		}
	})
}

func TestManifestRootBelowGitTopLevel(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{
		"lib/package.json":      "{}",
		"lib/c.ts":              "",
		"app/package.json":      "{}",
		"app/tsconfig.json":     "{}",
		"app/src/a.ts":          "",
		"app/src/a.test.ts":     "",
		"app/src/gen.ts":        "// Code generated by x. DO NOT EDIT.\n",
		"app/plain/no/pkg/d.ts": "",
	})
	git(t, top, "init", "-q")
	root := filepath.Join(top, "app")
	t.Chdir(root)

	sel, msg, err := selectUnits(root, top, []string{"tsconfig.json"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	want := map[string][]string{root: {"plain/no/pkg/d.ts", "src/a.test.ts", "src/a.ts"}}
	if !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("got %v want %v; with the manifest root below the git top-level, selected paths must stay root-relative and other top-level packages must not leak in", sel, want)
	}

	writeTree(t, top, map[string]string{".pnp.cjs": ""})
	sel, msg, err = selectUnits(root, top, []string{"src/a.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, ".pnp.cjs") || len(sel) != 0 {
		t.Fatalf(".pnp.cjs at the git top-level above the root: got %v %q; Plug'n'Play governs every package below it", sel, msg)
	}
}
