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

func TestUnitIsNearestPyprojectUpToTop(t *testing.T) {
	base := realTempDir(t)
	writeTree(t, base, map[string]string{"pyproject.toml": ""})
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		chain := rapid.SliceOfN(dirSegGen, 0, 4).Draw(t, "chain")
		want, wantOK := "", false
		for i := 0; i <= len(chain); i++ {
			if rapid.Bool().Draw(t, "pyproject") {
				dir := filepath.Join(append([]string{top}, chain[:i]...)...)
				writeTree(t, dir, map[string]string{"pyproject.toml": ""})
				want, wantOK = dir, true
			}
		}
		if rapid.Bool().Draw(t, "deeperPyproject") {
			writeTree(t, top, map[string]string{strings.Join(append(slices.Clone(chain), "z", "pyproject.toml"), "/"): ""})
		}
		if rapid.Bool().Draw(t, "siblingLockOnly") {
			writeTree(t, top, map[string]string{strings.Join(append(slices.Clone(chain), "uv.lock"), "/"): ""})
		}
		start := filepath.Join(append([]string{top}, chain...)...)
		got, ok := unitOf(top, start)
		if got != want || ok != wantOK {
			t.Fatalf("unitOf(%s) = %q,%v; want %q,%v; the unit decides which tests run against a change, so the nearest pyproject.toml must win, and one above the git top-level, below the file, or a bare uv.lock must not count", chain, got, ok, want, wantOK)
		}
	})
}

func TestNestedUnitsNearestWins(t *testing.T) {
	top := realTempDir(t)
	writeTree(t, top, map[string]string{
		"pyproject.toml":       "",
		"infra/pyproject.toml": "",
		"infra/stack.py":       "import os\n",
		"app/parse.py":         "import os\n",
	})
	t.Chdir(top)
	sel, msg, err := selectUnits(top, top, []string{"infra/stack.py", "app/parse.py"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	want := map[string][]string{
		filepath.Join(top, "infra"): {"infra/stack.py"},
		top:                         {"app/parse.py"},
	}
	if !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("got %v want %v; infra/stack.py belongs to infra's project, so running it under the root project uses the wrong dependencies and tests", sel, want)
	}
}

func drawPyprojectDirs(t *rapid.T, top string) map[string]bool {
	dirs := map[string]bool{}
	for _, d := range rapid.SliceOfN(rapid.SliceOfN(dirSegGen, 0, 2), 0, 4).Draw(t, "pyprojectDirs") {
		rel := strings.Join(d, "/")
		dirs[rel] = true
		writeTree(t, top, map[string]string{strings.TrimPrefix(rel+"/pyproject.toml", "/"): ""})
	}
	return dirs
}

var nonConfigPathGen = rapid.Custom(func(t *rapid.T) string {
	dirs := rapid.SliceOfN(dirSegGen, 0, 3).Draw(t, "dirs")
	return strings.Join(append(dirs, nameGen.Draw(t, "name")), "/")
}).Filter(func(p string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	return !slices.ContainsFunc(specConfigNames, func(c string) bool { return strings.EqualFold(base, c) })
})

func nearestPyprojectDir(dirs map[string]bool, p string) (string, bool) {
	for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
		if dirs[strings.TrimPrefix(dir, ".")] {
			return dir, true
		}
		if dir == "." {
			return "", false
		}
	}
}

func expectedSelection(top string, pyDirs, present map[string]bool, changed []string) (map[string][]string, []string) {
	want := map[string][]string{}
	var orphans []string
	for _, p := range changed {
		role, ok := specRole(p)
		if !ok {
			continue
		}
		dir, ok := nearestPyprojectDir(pyDirs, p)
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
		pyDirs := drawPyprojectDirs(t, top)
		changed := rapid.SliceOfN(nonConfigPathGen, 1, 5).Draw(t, "changed")
		present := map[string]bool{}
		for _, p := range changed {
			if rapid.Bool().Draw(t, "exists") {
				writeTree(t, top, map[string]string{p: "import os\n"})
				present[p] = true
			}
		}

		want, orphans := expectedSelection(top, pyDirs, present, changed)
		sel, msg, err := selectUnits(top, top, changed)
		if err != nil {
			t.Fatalf("selectUnits(%q): %v", changed, err)
		}
		if len(orphans) > 0 {
			if !strings.Contains(msg, "pyproject.toml") || !slices.ContainsFunc(orphans, func(o string) bool { return strings.Contains(msg, o) }) {
				t.Fatalf("changed %q with no pyproject.toml for %q: message %q; run must report environment naming the file and the missing pyproject.toml instead of skipping it", changed, orphans, msg)
			}
			return
		}
		if msg != "" || !maps.EqualFunc(sel, want, slices.Equal) {
			t.Fatalf("changed %q, pyproject.toml in %v:\n got %v %q\nwant %v\nevery unit with a changed claimed file must run, and only changed source and test files that still exist are selected (never .pyi stubs or unclaimed files)", changed, slices.Sorted(maps.Keys(pyDirs)), sel, msg, want)
		}
	})
}

func TestConfigChangeSelectsWholeUnit(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{
		"pyproject.toml":                         "",
		"uv.lock":                                "",
		"tools/root.py":                          "",
		"svc/pyproject.toml":                     "",
		"svc/uv.lock":                            "",
		"svc/app/parse.py":                       "",
		"svc/app/removed.py":                     "",
		"svc/app/env.pyi":                        "",
		"svc/app/models.py":                      "# Code generated by datamodel-codegen. DO NOT EDIT.\nimport os\n",
		"svc/tests/test_parse.py":                "",
		"svc/tests/conftest.py":                  "",
		"svc/deep/er/y.py":                       "",
		"svc/notes/analysis.ipynb":               "",
		"svc/plugins/inner/pyproject.toml":       "",
		"svc/plugins/inner/x.py":                 "",
		"svc/.gitignore":                         "build/\n",
		"svc/.venv/.gitignore":                   "*\n",
		"svc/.venv/lib/site-packages/dep/mod.py": "",
		"web/pyproject.toml":                     "",
		"web/b.py":                               "",
		"web/c.py":                               "",
	})
	git(t, top, "init", "-q")
	git(t, top, "add", ".")
	git(t, top, "commit", "-q", "-m", "base")
	if err := os.Rename(filepath.Join(top, "svc/app/removed.py"), filepath.Join(t.TempDir(), "removed.py")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, top, map[string]string{
		"svc/tests/new_test.py": "",
		"svc/build/lib/out.py":  "",
	})
	t.Chdir(top)

	sel, msg, err := selectUnits(top, top, []string{"svc/uv.lock", "web/b.py", "svc/app/parse.py"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	want := map[string][]string{
		filepath.Join(top, "svc"): {"svc/app/parse.py", "svc/deep/er/y.py", "svc/tests/conftest.py", "svc/tests/new_test.py", "svc/tests/test_parse.py"},
		filepath.Join(top, "web"): {"web/b.py"},
	}
	if !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("got %v\nwant %v\na uv.lock change can break any file of its unit, so every source and test file (tracked or untracked) must be re-checked; a nested unit's files, ignored files, the virtualenv, deleted, stub, and header-generated files are not that unit's code", sel, want)
	}

	sel, msg, err = selectUnits(top, top, []string{"pyproject.toml"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	if want := map[string][]string{top: {"tools/root.py"}}; !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("root pyproject.toml change: got %v want %v; the root unit must not absorb the nested projects", sel, want)
	}
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
		cfg := rapid.SampledFrom([]string{"uv.lock", "setup.cfg", "pytest.ini", "tox.ini", ".coveragerc"}).Draw(t, "config")
		writeTree(t, top, map[string]string{"pyproject.toml": "", rootRel + "/" + cfg: ""})
		files := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string {
			where := rapid.SampledFrom([]string{"", rootRel + "/", "other/", rootRel + "x/", "app/"}).Draw(t, "where")
			return where + nonConfigPathGen.Draw(t, "path")
		}), 1, 6).Draw(t, "files")
		want := []string{}
		for _, f := range files {
			writeTree(t, top, map[string]string{f: "import os\n"})
			if role, _ := specRole(f); strings.HasPrefix(f, rootRel+"/") && (role == "source" || role == "test") {
				want = append(want, strings.TrimPrefix(f, rootRel+"/"))
			}
		}
		slices.Sort(want)
		want = slices.Compact(want)
		git(t, top, "init", "-q")

		sel, msg, err := selectUnits(root, top, []string{cfg})
		if err != nil || msg != "" {
			t.Fatalf("selectUnits: %v %q", err, msg)
		}
		if w := map[string][]string{top: want}; !maps.EqualFunc(sel, w, slices.Equal) {
			t.Fatalf("root %s under a unit at the git top-level, %s changed, files %q:\n got %v\nwant %v\nassure evaluates only the manifest root, so a selected path outside it (../...) names a file no objective covers", rootRel, cfg, files, sel, w)
		}
	})
}

func TestSubpackageWithoutOwnLockfile(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	pkg := "python/sdk/opensearch"
	writeTree(t, top, map[string]string{
		"python/pyproject.toml":                      "",
		"python/uv.lock":                             "",
		"python/.venv/.gitignore":                    "*\n",
		"python/.venv/lib/site-packages/pulumi/x.py": "",
		pkg + "/pyproject.toml":                      "",
		pkg + "/pulumi_opensearch/x.py":              "",
		pkg + "/pulumi_opensearch/audit_config.py":   "# coding=utf-8\n# *** WARNING: this file was generated by pulumi-language-python. ***\nimport pulumi\n",
		pkg + "/tests/test_x.py":                     "",
	})
	git(t, top, "init", "-q")
	t.Chdir(top)

	unit := filepath.Join(top, pkg)
	sel, msg, err := selectUnits(top, top, []string{pkg + "/pulumi_opensearch/x.py"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	if want := map[string][]string{unit: {pkg + "/pulumi_opensearch/x.py"}}; !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("got %v want %v; a subpackage's own pyproject.toml makes it the unit even though its uv.lock and .venv live in python/", sel, want)
	}

	sel, msg, err = selectUnits(top, top, []string{pkg + "/pyproject.toml"})
	if err != nil || msg != "" {
		t.Fatalf("selectUnits: %v %q", err, msg)
	}
	if want := map[string][]string{unit: {pkg + "/pulumi_opensearch/x.py", pkg + "/tests/test_x.py"}}; !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("subpackage pyproject.toml change: got %v want %v; the Pulumi-generated audit_config.py must stay out of mutation and coverage", sel, want)
	}
}
