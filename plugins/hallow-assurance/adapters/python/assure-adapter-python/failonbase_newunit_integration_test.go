package main

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

// newUnitFiles is a unit that does not exist at the base commit: a pyproject, a src-layout
// package, a test that imports it at module level, and a test that needs nothing from it.
var newUnitFiles = map[string]string{
	"extra/pyproject.toml":            "[project]\nname = \"extra\"\nversion = \"0\"\nrequires-python = \">=3.11\"\n",
	"extra/src/newpkg/__init__.py":    "",
	"extra/src/newpkg/core.py":        "def double(x):\n    return 2 * x\n",
	"extra/tests/test_double.py":      "from newpkg.core import double\n\n\ndef test_double():\n    assert double(2) == 4\n",
	"extra/tests/test_independent.py": "def test_independent():\n    assert True\n",
}

// fobNewUnitRepo is fobRepo plus a second commit adding newUnitFiles, the shape of a pull request
// that adds a Python unit; the base is HEAD~1.
func fobNewUnitRepo(t *testing.T) string {
	top := fobRepo(t)
	writeTree(t, top, newUnitFiles)
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "add unit extra")
	return top
}

func runFailOnBaseFrom(t *testing.T, ref, out string) runResponse {
	t.Helper()
	code, raw, stderr := runPy("VER-FAIL-ON-BASE", ref, out)
	if code != 0 {
		t.Fatalf("run VER-FAIL-ON-BASE --changed-from %s exited %d: %s; adding a unit is an ordinary change, so the run must reach a verdict", ref, code, stderr)
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, raw); err != nil || len(vs) > 0 {
		t.Fatalf("response %s fails the adapter-run schema: %v %v", raw, err, vs)
	}
	var resp runResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func checkNewUnitCases(t *testing.T, j ev.JUnit, prefix string) {
	t.Helper()
	got := caseOutcomes(j)
	imp := prefix + "tests/test_double.py.test_double"
	plain := prefix + "tests/test_independent.py.test_independent"
	if want := []string{imp, plain}; !slices.Equal(slices.Sorted(maps.Keys(got)), want) || j.Cases != len(want) {
		t.Fatalf("cases %v (%d), want exactly %q; every test of a unit absent at base is new, so each is selected once", got, j.Cases, want)
	}
	if got[imp] != ev.Errored || !strings.Contains(failureText(j, imp), "newpkg") {
		t.Fatalf("cases %v, %s text %q; newpkg does not exist at base, so the test cannot be collected there and must error with pytest's import message (spec \"Test needs new function\")", got, imp, failureText(j, imp))
	}
	if got[plain] != ev.Passed {
		t.Fatalf("cases %v, %s text %q; test_independent needs nothing from the new unit, so pytest running it in the base tree must pass it; anything else means the base run did not execute", got, plain, failureText(j, plain))
	}
}

// TestIntegrationFailOnBaseNewUnitUnderRoot is DATA-173: a pull request adding a unit below the
// manifest root (the git top) must not error VER-FAIL-ON-BASE.
func TestIntegrationFailOnBaseNewUnitUnderRoot(t *testing.T) {
	top := fobNewUnitRepo(t)
	before := repoStateNoVenv(t, top)
	out := t.TempDir()
	j := failOnBaseOf(t, out, runFailOnBaseFrom(t, "HEAD~1", out))
	if after := repoStateNoVenv(t, top); after != before {
		t.Fatalf("the run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
	}
	checkNewUnitCases(t, j, "extra/")
}

// TestIntegrationFailOnBaseNewUnitIsRoot is DATA-173 with the manifest root being the new unit
// itself, so the root has no counterpart in the base tree.
func TestIntegrationFailOnBaseNewUnitIsRoot(t *testing.T) {
	top := fobNewUnitRepo(t)
	t.Chdir(filepath.Join(top, "extra"))
	out := t.TempDir()
	j := failOnBaseOf(t, out, runFailOnBaseFrom(t, "HEAD~1", out))
	checkNewUnitCases(t, j, "")
}
