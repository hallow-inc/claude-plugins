package main

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

const uvFixturePyproject = `[project]
name = "fixture"
version = "0"
requires-python = ">=3.11"
dependencies = []

[dependency-groups]
dev = ["pytest>=9,<10", "pytest-randomly>=5,<6", "hypothesis>=6.130,<7", "coverage>=7.13,<8", "pytest-cov>=7,<8", "cosmic-ray>=8.7,<9", "parso>=0.8.7,<0.9"]

[tool.pytest.ini_options]
pythonpath = ["."]
`

var (
	uvOnce    sync.Once
	uvProject string
	uvErr     error
	uvSkip    string
)

// syncedProject is a uv project with pytest, pytest-randomly and Hypothesis
// installed, synced once per process. Tests copy its pyproject.toml and
// uv.lock and point UV_PROJECT_ENVIRONMENT at its .venv.
func syncedProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not on PATH; VER-TESTS-PASS integration tests run real pytest in a virtualenv built with `uv sync`")
	}
	uvOnce.Do(func() {
		uvProject, uvErr = os.MkdirTemp("", "assure-py-uvproj-*")
		if uvErr != nil {
			return
		}
		if uvErr = os.WriteFile(filepath.Join(uvProject, "pyproject.toml"), []byte(uvFixturePyproject), 0o644); uvErr != nil {
			return
		}
		cmd := exec.CommandContext(context.Background(), "uv", "sync", "--quiet")
		cmd.Dir = uvProject
		cmd.Env = append(os.Environ(), "UV_PROJECT_ENVIRONMENT=")
		if out, err := cmd.CombinedOutput(); err != nil {
			uvSkip = fmt.Sprintf("uv sync of the pytest fixture failed, most likely offline with a cold uv cache: %v\n%s", err, out)
		}
	})
	if uvErr != nil {
		t.Fatalf("could not create the uv fixture: %v", uvErr)
	}
	if uvSkip != "" {
		t.Skip(uvSkip)
	}
	return uvProject
}

var realPyFiles = map[string]string{
	".gitignore":      ".venv/\n.hypothesis/\n",
	"README.md":       "readme\n",
	"app/__init__.py": "",
	"app/parse.py":    "def parse(s):\n    return int(s)\n",
	"tests/test_cli.py": `from app.parse import parse


def test_parses_int():
    assert parse("3") == 3


def test_parses_negative():
    assert parse("-3") == -3


class TestCli:
    def test_parses_zero(self):
        assert parse("0") == 0
`,
	"tests/test_other.py": "def test_other():\n    assert True\n",
}

// realPyRepo commits a one-unit repository using the synced project's lock
// and virtualenv, and enters it.
func realPyRepo(t *testing.T, extra map[string]string) string {
	proj := syncedProject(t)
	isolateGit(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", filepath.Join(proj, ".venv"))
	top := realTempDir(t)
	files := maps.Clone(realPyFiles)
	maps.Copy(files, extra)
	for _, name := range []string{"pyproject.toml", "uv.lock"} {
		data, err := os.ReadFile(filepath.Join(proj, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	writeTree(t, top, files)
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	t.Chdir(top)
	return top
}

func venvPythonVersion(t *testing.T) string {
	out, err := exec.CommandContext(t.Context(), filepath.Join(syncedProject(t), ".venv", "bin", "python"), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "Python ")
}

func TestIntegrationSourceOnlyChangeRunsUnitTests(t *testing.T) {
	top := realPyRepo(t, nil)
	writeTree(t, top, map[string]string{"app/parse.py": "def parse(s):\n    return int(s.strip())\n"})
	out := t.TempDir()
	resp := runTestsIn(t, out)
	if got := resp.ToolVersions["python"]; got != venvPythonVersion(t) {
		t.Fatalf("tool_versions python %q, want the virtualenv's %q", got, venvPythonVersion(t))
	}
	for _, tool := range []string{"pytest", "pytest-randomly", "hypothesis"} {
		if resp.ToolVersions[tool] == "" || resp.PinnedBy[tool] != "uv.lock" {
			t.Fatalf("tool_versions %v pinned_by %v; every tool VER-TESTS-PASS runs is recorded with the lockfile that pins it", resp.ToolVersions, resp.PinnedBy)
		}
	}
	want := map[string]ev.Outcome{
		"tests/test_cli.py.test_parses_int":           ev.Passed,
		"tests/test_cli.py.test_parses_negative":      ev.Passed,
		"tests/test_cli.py.TestCli::test_parses_zero": ev.Passed,
		"tests/test_other.py.test_other":              ev.Passed,
	}
	if got := caseOutcomes(junitOf(t, out, resp)); !maps.Equal(got, want) {
		t.Fatalf("cases %v\nwant %v; only app/parse.py changed, and the spec runs the unit's whole suite, so tests/test_cli.py's three cases and the rest of the unit must run", got, want)
	}
}

func TestIntegrationCollectionErrorIsErroringCase(t *testing.T) {
	top := realPyRepo(t, nil)
	writeTree(t, top, map[string]string{"tests/test_broken.py": "import module_that_does_not_exist\n\n\ndef test_never():\n    pass\n"})
	out := t.TempDir()
	j := junitOf(t, out, runTestsIn(t, out))
	got := caseOutcomes(j)
	if got["tests/test_broken.py.(collection)"] != ev.Errored || got["tests/test_cli.py.test_parses_int"] != ev.Passed {
		t.Fatalf("cases %v; the unimportable file must be one erroring case, and the other files must still run (--continue-on-collection-errors)", got)
	}
	if j.Errors != 1 || j.Failures != 0 {
		t.Fatalf("%d errors %d failures, want 1 and 0", j.Errors, j.Failures)
	}
	if text := failureText(j, "tests/test_broken.py.(collection)"); !strings.Contains(text, "No module named 'module_that_does_not_exist'") {
		t.Fatalf("collection case text %q; the spec requires pytest's message so the developer sees which import broke", text)
	}
}

func TestIntegrationNoChangedPythonFilesIsEmpty(t *testing.T) {
	top := realPyRepo(t, nil)
	writeTree(t, top, map[string]string{"README.md": "changed\n"})
	out := t.TempDir()
	if j := junitOf(t, out, runTestsIn(t, out)); j.Cases != 0 {
		t.Fatalf("JUnit %+v; only README.md changed, and the spec wants a JUnit file with zero cases", j)
	}
}

func TestIntegrationNothingCollectedIsEmpty(t *testing.T) {
	top := realPyRepo(t, nil)
	git(t, top, "rm", "-q", "tests/test_cli.py", "tests/test_other.py")
	git(t, top, "commit", "-q", "-m", "no tests")
	writeTree(t, top, map[string]string{"app/parse.py": "def parse(s):\n    return 0\n"})
	out := t.TempDir()
	if j := junitOf(t, out, runTestsIn(t, out)); j.Cases != 0 {
		t.Fatalf("JUnit %+v; pytest collects nothing in this unit (exit 5), which the spec makes zero cases, not an error", j)
	}
}

const orderConftest = `import os

import pytest


@pytest.fixture(autouse=True)
def _record(request):
    with open(os.environ["ASSURE_TEST_LOG"], "a") as f:
        f.write("%s %s\n" % (request.node.nodeid, os.environ.get("ASSURE_SEED", "")))
    yield
`

const hypothesisTest = `import os

from hypothesis import given, settings, strategies as st


@settings(max_examples=25, database=None)
@given(st.integers())
def test_draws(n):
    with open(os.environ["ASSURE_DRAW_LOG"], "a") as f:
        f.write("%d\n" % n)
`

func orderFixture() map[string]string {
	files := map[string]string{"tests/conftest.py": orderConftest, "tests/test_hyp.py": hypothesisTest}
	for _, m := range []string{"a", "b"} {
		var b strings.Builder
		for i := range 8 {
			fmt.Fprintf(&b, "def test_%s%d():\n    pass\n\n\n", m, i)
		}
		files["tests/test_order_"+m+".py"] = b.String()
	}
	return files
}

type seededRun struct {
	order, seeds []string
	draws        string
}

func runLogged(t *testing.T, ref string) seededRun {
	logs := t.TempDir()
	testLog, drawLog := filepath.Join(logs, "tests"), filepath.Join(logs, "draws")
	t.Setenv("ASSURE_TEST_LOG", testLog)
	t.Setenv("ASSURE_DRAW_LOG", drawLog)
	out := t.TempDir()
	if j := junitOf(t, out, runTestsFrom(t, ref, out)); j.Failures+j.Errors != 0 {
		t.Fatalf("fixture tests failed: %+v", j.Failing)
	}
	data, err := os.ReadFile(testLog)
	if err != nil {
		t.Fatal(err)
	}
	draws, err := os.ReadFile(drawLog)
	if err != nil {
		t.Fatal(err)
	}
	var r seededRun
	for line := range strings.Lines(string(data)) {
		id, seed, _ := strings.Cut(strings.TrimSpace(line), " ")
		r.order = append(r.order, id)
		r.seeds = append(r.seeds, seed)
	}
	r.draws = string(draws)
	return r
}

// TestIntegrationSeedFixesOrderAndDraws checks that -p randomly and the seed
// flags reach pytest: one commit gives one order and one set of Hypothesis
// draws, so a failure replays; a different commit gives a different order,
// so order-dependent tests surface.
func TestIntegrationSeedFixesOrderAndDraws(t *testing.T) {
	top := realPyRepo(t, orderFixture())
	writeTree(t, top, map[string]string{"app/parse.py": "def parse(s):\n    return int(s, 10)\n"})
	first, second := runLogged(t, "HEAD"), runLogged(t, "HEAD")
	if !slices.Equal(first.order, second.order) || first.draws != second.draws {
		t.Fatalf("two runs at one HEAD differ:\n%q\n%q\ndraws equal: %v; the order and Hypothesis inputs must follow the commit's seed so a failure replays", first.order, second.order, first.draws == second.draws)
	}
	defined := slices.Clone(first.order)
	slices.Sort(defined)
	if slices.Equal(first.order, defined) {
		t.Fatalf("tests ran in definition order %q; pytest-randomly must shuffle them", first.order)
	}
	seed := checkSeed(t, headOf(t, top))
	for _, s := range first.seeds {
		if s != seed {
			t.Fatalf("a test saw ASSURE_SEED=%q, want %q (from HEAD)", s, seed)
		}
	}
	git(t, top, "commit", "-q", "-am", "next")
	third := runLogged(t, "HEAD~1")
	if slices.Equal(first.order, third.order) || first.draws == third.draws {
		t.Fatalf("a new HEAD ran the same order %q (draws equal: %v); the seed must come from the commit, not a constant", third.order, first.draws == third.draws)
	}
	if third.seeds[0] != checkSeed(t, headOf(t, top)) {
		t.Fatalf("ASSURE_SEED=%q after the commit, want the new HEAD's seed", third.seeds[0])
	}
}

func TestIntegrationNoPytestCache(t *testing.T) {
	top := realPyRepo(t, map[string]string{"tests/test_fail.py": "def test_fails():\n    assert False\n"})
	writeTree(t, top, map[string]string{"app/parse.py": "def parse(s):\n    return 1\n"})
	out := t.TempDir()
	j := junitOf(t, out, runTestsIn(t, out))
	if j.Failures == 0 {
		t.Fatalf("JUnit %+v; the fixture has failing tests, which is what pytest's cache would record", j)
	}
	err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".pytest_cache" {
			return fmt.Errorf("%s exists", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v; the run must not write pytest's cache into the repository (-p no:cacheprovider), or --lf state from one run changes the next", err)
	}
}

// TestIntegrationPluginAutoloadOff: D3 loads both seeded plugins explicitly
// so PYTEST_DISABLE_PLUGIN_AUTOLOAD cannot drop shuffling. Without the
// Hypothesis plugin loaded, pytest rejects --hypothesis-seed (exit 4) and
// every VER-TESTS-PASS run in such a repository fails.
func TestIntegrationPluginAutoloadOff(t *testing.T) {
	top := realPyRepo(t, orderFixture())
	writeTree(t, top, map[string]string{"app/parse.py": "def parse(s):\n    return int(s, 10)\n"})
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
	code, _, stderr := runPy("VER-TESTS-PASS", "HEAD", t.TempDir())
	if code != 0 {
		t.Fatalf("run exited %d with plugin autoload off: %s; D3 loads pytest-randomly and Hypothesis's plugin (-p hypothesispytest) explicitly", code, stderr)
	}
	r := runLogged(t, "HEAD")
	defined := slices.Clone(r.order)
	slices.Sort(defined)
	if slices.Equal(r.order, defined) {
		t.Fatalf("tests ran in definition order %q with autoload off; shuffling must not depend on autoload", r.order)
	}
}

// editableRepo is a committed uv library project whose own package is
// installed editable into its .venv (uv's default for a project with a build
// backend), so the virtualenv imports demo from the repository's src/.
// demo.VALUE is "base" at the returned commit and "current" in the worktree.
func editableRepo(t *testing.T) (top, base, python string) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not on PATH; the editable-install tests build a real uv project")
	}
	isolateGit(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	t.Setenv("PYTHONPATH", "")
	top = realTempDir(t)
	uv := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "uv", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("uv %v failed, most likely offline with a cold uv cache: %v\n%s", args, err, out)
		}
	}
	uv("init", "--lib", "--name", "demo", "--vcs", "none", "--no-readme", "--quiet", "--python", ">=3.11", ".")
	writeTree(t, top, map[string]string{".gitignore": ".venv/\n", "src/demo/__init__.py": "VALUE = \"base\"\n"})
	uv("sync", "--quiet")
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	base = strings.TrimSpace(gitQuiet(t, top, "rev-parse", "HEAD"))
	writeTree(t, top, map[string]string{"src/demo/__init__.py": "VALUE = \"current\"\n"})
	return top, base, filepath.Join(top, ".venv", "bin", "python")
}

func importDemo(t *testing.T, python, dir string, env []string) (value, file string) {
	t.Helper()
	stdout, stderr, code, err := runToolEnv(dir, env, python, "-c", "import demo; print(demo.VALUE); print(demo.__file__)")
	if err != nil || code != 0 {
		t.Fatalf("import demo: exit %d %v\n%s", code, err, stderr)
	}
	value, file, _ = strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	return value, file
}

// TestIntegrationEditableInstallImportsFromTree is task 6.2 and the spec's
// "Editable install does not leak current code": under treeEnv the base
// tree's demo is the one imported, though the virtualenv's .pth points at the
// repository, and the probe agrees there is no leak.
func TestIntegrationEditableInstallImportsFromTree(t *testing.T) {
	top, base, python := editableRepo(t)
	elsewhere := t.TempDir()
	if value, _ := importDemo(t, python, elsewhere, nil); value != "current" {
		t.Fatalf("without treeEnv the virtualenv imports demo.VALUE=%q, want \"current\"; the fixture must really be an editable install of the repository, or this test cannot see a leak", value)
	}
	dst := filepath.Join(t.TempDir(), "tree")
	if err := baseTree(top, base, top, dst); err != nil {
		t.Fatal(err)
	}
	value, file := importDemo(t, python, elsewhere, treeEnv(dst))
	if value != "base" || !strings.HasPrefix(realPath(file), realPath(dst)+string(filepath.Separator)) {
		t.Fatalf("under treeEnv demo.VALUE=%q from %s, want \"base\" from the tree %s; otherwise fail-on-base runs the new test against the current code and a regression test passes on base", value, file, dst)
	}
	if msg, err := probeTree(python, top, dst, dst); err != nil || msg != "" {
		t.Fatalf("probeTree = %q, %v; demo resolves inside the tree, so there is nothing to report", msg, err)
	}
}

// TestIntegrationMetaPathEditableLeakReported: a build backend that installs
// a meta-path finder (a .pth that imports a module inserting itself at the
// front of sys.meta_path) defeats the PYTHONPATH prepend; the probe must
// report it rather than let tests run the repository's code.
func TestIntegrationMetaPathEditableLeakReported(t *testing.T) {
	top, base, python := editableRepo(t)
	site, err := sitePackages(filepath.Join(top, ".venv"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(top, "src", "demo")
	writeTree(t, site, map[string]string{
		"_assure_leak.pth":       "import _assure_leak_finder\n",
		"_assure_leak_finder.py": fmt.Sprintf(leakFinder, filepath.Join(pkg, "__init__.py"), pkg),
	})
	dst := filepath.Join(t.TempDir(), "tree")
	if err := baseTree(top, base, top, dst); err != nil {
		t.Fatal(err)
	}
	if value, _ := importDemo(t, python, t.TempDir(), treeEnv(dst)); value != "current" {
		t.Fatalf("with the finder installed demo.VALUE=%q under treeEnv, want \"current\"; the fixture must defeat the prepend for this test to mean anything", value)
	}
	msg, err := probeTree(python, top, dst, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "demo") || !strings.Contains(msg, pkg) {
		t.Fatalf("probeTree = %q; demo imports from %s, outside the tree, and D6 makes that an environment failure naming the package", msg, pkg)
	}
}
