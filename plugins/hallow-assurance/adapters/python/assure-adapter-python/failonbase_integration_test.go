package main

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const fobRngBase = "def in_range(x, lo, hi):\n    return lo <= x < hi\n"

const fobTestsBase = `import pytest

from demo.rng import in_range


def test_old():
    assert in_range(1, 0, 2)


def test_parses():
    assert in_range(0, 0, 2)


class TestRange:
    def test_inner(self):
        assert in_range(1, 0, 3)
`

// The fix: in_range now includes its end, clamp is new. test_old and
// TestRange::test_inner are unchanged, test_parses is edited but passes on
// base, test_includes_end and test_each are new regression tests, and
// fobClamp is a new test file needing a function the base lacks.
const fobRngFix = "def in_range(x, lo, hi):\n    return lo <= x <= hi\n\n\ndef clamp(x):\n    return x\n"

const fobTestsFix = `import pytest

from demo.rng import in_range


def test_old():
    assert in_range(1, 0, 2)


def test_parses():
    assert in_range(1, 0, 2)


class TestRange:
    def test_inner(self):
        assert in_range(1, 0, 3)

    def test_includes_end(self):
        assert in_range(2, 0, 2)


@pytest.mark.parametrize("x", [0, 1, 2])
def test_each(x):
    assert in_range(x, 0, 2)
`

const fobClamp = "from demo.rng import clamp\n\n\ndef test_clamp():\n    assert clamp(1) == 1\n"

// fobRepo is a committed uv library project, installed editable into its
// own .venv with pytest, pytest-randomly and Hypothesis, holding the base
// code and tests; the caller applies the change.
func fobRepo(t *testing.T) string {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not on PATH; fail-on-base integration tests build a real uv project")
	}
	isolateGit(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	t.Setenv("PYTHONPATH", "")
	top := realTempDir(t)
	uv := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "uv", args...)
		cmd.Dir = top
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("uv %v failed, most likely offline with a cold uv cache: %v\n%s", args, err, out)
		}
	}
	uv("init", "--lib", "--name", "demo", "--vcs", "none", "--no-readme", "--quiet", "--python", ">=3.11", ".")
	writeTree(t, top, map[string]string{
		".gitignore":              ".venv/\n.hypothesis/\n",
		"src/demo/rng.py":         fobRngBase,
		"tests/test_rng.py":       fobTestsBase,
		"tests/test_untouched.py": "def test_u():\n    assert True\n",
	})
	uv("add", "--dev", "--quiet", "pytest>=9,<10", "pytest-randomly>=5,<6", "hypothesis>=6.130,<7", "coverage>=7.13,<8", "pytest-cov>=7,<8", "cosmic-ray>=8.7,<9", "parso>=0.8.7,<0.9")
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	t.Chdir(top)
	return top
}

func runObjectiveIn(t *testing.T, obj, out string) runResponse {
	t.Helper()
	code, raw, stderr := runPy(obj, "HEAD", out)
	if code != 0 {
		t.Fatalf("run %s exited %d: %s", obj, code, stderr)
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, raw); err != nil || len(vs) > 0 {
		t.Fatalf("run %s: response %s fails the adapter-run schema, so assure discards it: %v %v", obj, raw, err, vs)
	}
	var resp runResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func failOnBaseOf(t *testing.T, out string, resp runResponse) ev.JUnit {
	t.Helper()
	if resp.Environment != nil {
		t.Fatalf("environment %q", resp.Environment.Message)
	}
	if want := []evidence{{Type: "test.fail_on_base", Path: "fail-on-base.xml"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "fail-on-base.xml"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("assure's JUnit reader rejects the evidence: %v\n%s", err, data)
	}
	return j
}

func TestIntegrationFailOnBaseScenarios(t *testing.T) {
	top := fobRepo(t)
	writeTree(t, top, map[string]string{"src/demo/rng.py": fobRngFix, "tests/test_rng.py": fobTestsFix})
	python := filepath.Join(top, ".venv", "bin", "python")
	stdout, _, _, err := runToolEnv(t.TempDir(), nil, python, "-c", "from demo.rng import in_range; print(in_range(2, 0, 2))")
	if err != nil || strings.TrimSpace(string(stdout)) != "True" {
		t.Fatalf("the virtualenv imports %q (%v) for in_range(2, 0, 2) outside the repository, want True; the fixture must be an editable install of the current code for the leak scenario to mean anything", stdout, err)
	}
	before := repoStateNoVenv(t, top)
	out := t.TempDir()
	resp := runObjectiveIn(t, "VER-FAIL-ON-BASE", out)
	after := repoStateNoVenv(t, top)
	j := failOnBaseOf(t, out, resp)
	got := caseOutcomes(j)

	t.Run("Repository untouched", func(t *testing.T) {
		if before != after {
			t.Fatalf("the run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
	t.Run("tool_versions", func(t *testing.T) {
		if resp.ToolVersions["pytest"] == "" || resp.ToolVersions["python"] == "" {
			t.Fatalf("tool_versions %v; the spec requires pytest, and python is recorded for every pytest run", resp.ToolVersions)
		}
	})
	checkFobCases(t, got, j.Cases)
}

func checkFobCases(t *testing.T, got map[string]ev.Outcome, cases int) {
	t.Run("Regression test fails on base", func(t *testing.T) {
		if got["tests/test_rng.py.TestRange::test_includes_end"] != ev.Failed {
			t.Fatalf("cases %v; test_includes_end checks the fixed end bound, so on the base code it must fail", got)
		}
	})
	t.Run("Editable install does not leak current code", func(t *testing.T) {
		if got["tests/test_rng.py.TestRange::test_includes_end"] != ev.Failed || got["tests/test_rng.py.test_each"] != ev.Failed {
			t.Fatalf("cases %v; these tests pass on the current code the virtualenv's editable install points at, so a pass means the base tree's demo was not the one imported", got)
		}
	})
	t.Run("Edited test is selected", func(t *testing.T) {
		if got["tests/test_rng.py.test_parses"] != ev.Passed {
			t.Fatalf("cases %v; test_parses's body changed, so it is selected, and it passes on base", got)
		}
	})
	t.Run("Unchanged test is not selected", func(t *testing.T) {
		for _, name := range []string{"tests/test_rng.py.test_old", "tests/test_rng.py.TestRange::test_inner", "tests/test_untouched.py.test_u"} {
			if _, ok := got[name]; ok {
				t.Fatalf("cases %v include %s, whose text did not change; fail-on-base would demand that an old passing test fail", got, name)
			}
		}
	})
	t.Run("Failing parameter instance fails the case", func(t *testing.T) {
		for name := range got {
			if strings.Contains(name, "[") {
				t.Fatalf("cases %v hold a parameter instance %s; instances fold into their test's case", got, name)
			}
		}
		if got["tests/test_rng.py.test_each"] != ev.Failed {
			t.Fatalf("cases %v; test_each[2] fails on base, so test_each's one case fails", got)
		}
	})
	t.Run("one case per selected test", func(t *testing.T) {
		want := []string{"tests/test_rng.py.TestRange::test_includes_end", "tests/test_rng.py.test_each", "tests/test_rng.py.test_parses"}
		if names := slices.Sorted(maps.Keys(got)); !slices.Equal(names, want) || cases != len(want) {
			t.Fatalf("cases %q (%d), want exactly %q", names, cases, want)
		}
	})
}

// TestIntegrationFailOnBaseTestNeedsNewFunction is the spec scenario "Test
// needs new function" with the usual module-level import: the new test file
// cannot be collected on base. pytest exits 4 ("found no collectors") when a
// node id's module fails to collect, so the adapter must still give the
// erroring case rather than fail the run, and keep the other file's case.
func TestIntegrationFailOnBaseTestNeedsNewFunction(t *testing.T) {
	top := fobRepo(t)
	edited := strings.Replace(fobTestsBase, "assert in_range(0, 0, 2)", "assert in_range(1, 0, 2)", 1)
	writeTree(t, top, map[string]string{"src/demo/rng.py": fobRngFix, "tests/test_rng.py": edited, "tests/test_clamp.py": fobClamp})
	out := t.TempDir()
	j := failOnBaseOf(t, out, runObjectiveIn(t, "VER-FAIL-ON-BASE", out))
	got := caseOutcomes(j)
	name := "tests/test_clamp.py.test_clamp"
	if got[name] != ev.Errored || !strings.Contains(failureText(j, name), "cannot import name 'clamp'") {
		t.Fatalf("cases %v, %s text %q; clamp does not exist on base, so the test cannot load there and must error with pytest's message", got, name, failureText(j, name))
	}
	if got["tests/test_rng.py.test_parses"] != ev.Passed || len(got) != 2 {
		t.Fatalf("cases %v; the edited test_parses in the other file must still run and pass", got)
	}
}

func TestIntegrationFailOnBaseCustomDiscoveryRefused(t *testing.T) {
	top := fobRepo(t)
	data, err := os.ReadFile(filepath.Join(top, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, top, map[string]string{"pyproject.toml": string(data) + "\n[tool.pytest.ini_options]\npython_functions = \"check_*\"\n"})
	git(t, top, "commit", "-q", "-am", "custom discovery")
	writeTree(t, top, map[string]string{"tests/test_rng.py": fobTestsFix})
	before := repoStateNoVenv(t, top)
	resp := runObjectiveIn(t, "VER-FAIL-ON-BASE", t.TempDir())
	if resp.Environment == nil || !strings.Contains(resp.Environment.Message, "pyproject.toml") || !strings.Contains(resp.Environment.Message, "python_functions") {
		t.Fatalf("response %+v; spec \"Custom discovery refused\": with python_functions set the adapter cannot know which functions are tests, so it reports environment naming pyproject.toml and python_functions", resp)
	}
	if after := repoStateNoVenv(t, top); after != before {
		t.Fatalf("a refused run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// repoStateNoVenv is repoState without the ignored virtualenv, where
// Python may write bytecode caches for its own packages.
func repoStateNoVenv(t *testing.T, top string) string {
	venv := filepath.Join(top, ".venv")
	tmp := filepath.Join(filepath.Dir(top), filepath.Base(top)+"-venv-aside")
	if err := os.Rename(venv, tmp); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(tmp, venv); err != nil {
			t.Fatal(err)
		}
	}()
	return repoState(t, top)
}
