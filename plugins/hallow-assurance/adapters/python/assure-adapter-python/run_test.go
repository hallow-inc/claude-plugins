package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func exe(t fataler, p, src string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(p, []byte(src), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("%v", err)
	}
}

// installFakePython replaces the venv's interpreter with a script that
// prints version for --version and otherwise records its argv, working
// directory and ASSURE_SEED into data, copies data/report to the
// --junitxml path, prints data/stdout and exits with data/code.
func installFakePython(t fataler, venv, data, version string) {
	writeTree(t, data, map[string]string{"version": "Python " + version + "\n"})
	exe(t, filepath.Join(venv, "bin", "python"), fmt.Sprintf(`#!/bin/sh
d='%s'
if [ "$1" = "--version" ]; then /bin/cat "$d/version"; exit 0; fi
pwd -P > "$d/cwd"
printf '%%s\n' "$@" > "$d/args"
printf '%%s' "$ASSURE_SEED" > "$d/seed"
for a; do case "$a" in --junitxml=*) [ -f "$d/report" ] && /bin/cat "$d/report" > "${a#--junitxml=}";; esac; done
[ -f "$d/stdout" ] && /bin/cat "$d/stdout"
exit $(/bin/cat "$d/code" 2>/dev/null || echo 0)
`, data))
}

type pyReplay struct {
	report   string
	noReport bool
	stdout   string
	code     int
}

func replayPytest(t fataler, data string, r pyReplay) {
	for _, f := range []string{"report", "args", "cwd", "seed"} {
		_ = os.Remove(filepath.Join(data, f))
	}
	files := map[string]string{"stdout": r.stdout, "code": fmt.Sprint(r.code)}
	if !r.noReport {
		files["report"] = r.report
	}
	writeTree(t, data, files)
}

type pyCall struct {
	invoked   bool
	args      []string
	cwd, seed string
}

func pytestCall(t fataler, data string) pyCall {
	args, err := os.ReadFile(filepath.Join(data, "args"))
	if os.IsNotExist(err) {
		return pyCall{}
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	cwd, _ := os.ReadFile(filepath.Join(data, "cwd"))
	seed, _ := os.ReadFile(filepath.Join(data, "seed"))
	return pyCall{invoked: true, args: strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"), cwd: strings.TrimSpace(string(cwd)), seed: string(seed)}
}

var fakeUnitFiles = map[string]string{
	"pyproject.toml":    "[project]\nname = \"x\"\nversion = \"0\"\n",
	"uv.lock":           "",
	"app/__init__.py":   "",
	"app/parse.py":      "def parse(s):\n    return int(s)\n",
	"tests/test_cli.py": "def test_parse():\n    pass\n",
	"README.md":         "readme\n",
}

type pyRepo struct{ top, data string }

// newPyRepo commits a repository with one unit at each of units (relative
// to top), each with an ignored .venv holding the resolved tools and a fake
// interpreter at the given version, and enters it.
func newPyRepo(t *testing.T, units map[string]string) pyRepo {
	isolateGit(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	r := pyRepo{top: realTempDir(t), data: t.TempDir()}
	writeTree(t, r.top, map[string]string{".gitignore": ".venv/\n"})
	for unit, version := range units {
		dir := filepath.Join(r.top, filepath.FromSlash(unit))
		writeTree(t, dir, fakeUnitFiles)
		installAllTools(t, filepath.Join(dir, ".venv"), nil)
		installFakePython(t, filepath.Join(dir, ".venv"), r.dataOf(unit), version)
	}
	git(t, r.top, "init", "-q")
	git(t, r.top, "add", "-A")
	git(t, r.top, "commit", "-q", "-m", "base")
	t.Chdir(r.top)
	return r
}

func (r pyRepo) dataOf(unit string) string {
	return filepath.Join(r.data, filepath.FromSlash(unit))
}

func runPy(obj, ref, out string) (int, []byte, string) {
	var o, e bytes.Buffer
	code := run([]string{"run", obj, "--changed-from", ref, "--out", out}, &o, &e)
	return code, o.Bytes(), e.String()
}

func runTestsIn(t fataler, out string) runResponse {
	return runTestsFrom(t, "HEAD", out)
}

func runTestsFrom(t fataler, ref, out string) runResponse {
	code, raw, stderr := runPy("VER-TESTS-PASS", ref, out)
	if code != 0 {
		t.Fatalf("run VER-TESTS-PASS exited %d: %s; a run that reaches a verdict or an environment problem exits 0", code, stderr)
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, raw); err != nil || len(vs) > 0 {
		t.Fatalf("run VER-TESTS-PASS: response %s fails the adapter-run schema, so assure discards it: %v %v", raw, err, vs)
	}
	var resp runResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v", err)
	}
	if resp.Protocol != 2 {
		t.Fatalf("protocol %d in %s; assure reads run responses as protocol 2", resp.Protocol, raw)
	}
	return resp
}

func junitOf(t fataler, out string, resp runResponse) ev.JUnit {
	if resp.Environment != nil {
		t.Fatalf("environment %q; the fixture has every tool VER-TESTS-PASS needs", resp.Environment.Message)
	}
	if want := []evidence{{Type: "test.junit", Path: "junit.xml"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v; VER-TESTS-PASS reads exactly this entry", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatalf("evidence names junit.xml but it was not written: %v", err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("assure's JUnit reader rejects the evidence: %v\n%s", err, data)
	}
	return j
}

func caseOutcomes(j ev.JUnit) map[string]ev.Outcome {
	got := map[string]ev.Outcome{}
	for _, s := range j.Suites {
		for _, c := range s.Cases {
			got[c.Name] = c.Outcome
		}
	}
	return got
}

func failureText(j ev.JUnit, name string) string {
	for _, f := range j.Failing {
		if f.Name == name {
			return f.Text
		}
	}
	return ""
}

func xunit1(cases ...string) string {
	return `<?xml version="1.0" encoding="utf-8"?><testsuites name="pytest tests"><testsuite name="pytest">` + strings.Join(cases, "") + `</testsuite></testsuites>`
}

const (
	passCase    = `<testcase classname="tests.test_cli" name="test_parse" file="tests/test_cli.py" line="0" time="0.001" />`
	failCase    = `<testcase classname="tests.test_cli.TestCli" name="test_neg[-1]" file="tests/test_cli.py" line="4" time="0.001"><failure message="assert -1 == 1">E   assert -1 == 1</failure></testcase>`
	collectCase = `<testcase classname="" name="tests.test_broken" file="tests/test_broken.py" time="0.000"><error message="collection failure">E   ModuleNotFoundError: No module named 'nope'</error></testcase>`
)

// wantPytestArgs is the D4 command line; the junit path is the one argument
// the adapter picks.
func wantPytestArgs(junit, seed string) []string {
	return []string{"-m", "pytest", "-p", "no:cacheprovider", "--continue-on-collection-errors",
		"--junitxml=" + junit, "-o", "junit_family=xunit1", "-q",
		"-p", "randomly", "-p", "hypothesispytest", "--randomly-seed=" + seed, "--hypothesis-seed=" + seed}
}

func junitArg(args []string) string {
	for _, a := range args {
		if p, ok := strings.CutPrefix(a, "--junitxml="); ok {
			return p
		}
	}
	return ""
}

func TestRunTestsRunsWholeUnitWithSeed(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	writeTree(t, r.top, map[string]string{"app/parse.py": "def parse(s):\n    return int(s.strip())\n"})
	replayPytest(t, r.dataOf("."), pyReplay{report: xunit1(passCase, failCase, collectCase), stdout: "PYTEST OUTPUT\n", code: 1})
	out := filepath.Join(t.TempDir(), "nested", "out")
	resp := runTestsIn(t, out)

	want := map[string]string{"python": "3.13.9", "pytest": specRanges["pytest"][0], "pytest-randomly": specRanges["pytest-randomly"][0], "hypothesis": specRanges["hypothesis"][0]}
	if !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v; the spec records python and exactly the tools VER-TESTS-PASS uses", resp.ToolVersions, want)
	}
	if want := map[string]string{"pytest": "uv.lock", "pytest-randomly": "uv.lock", "hypothesis": "uv.lock"}; !maps.Equal(resp.PinnedBy, want) {
		t.Fatalf("pinned_by %v, want %v; python itself is not pinned by uv.lock", resp.PinnedBy, want)
	}
	if len(resp.Untracked) != 0 {
		t.Fatalf("untracked %v with a tracked uv.lock", resp.Untracked)
	}

	c := pytestCall(t, r.dataOf("."))
	if c.cwd != r.top {
		t.Fatalf("pytest ran in %s, want the unit %s; the adopter's rootdir, testpaths and conftest.py apply only from there", c.cwd, r.top)
	}
	seed := checkSeed(t, headOf(t, r.top))
	junit := junitArg(c.args)
	if !slices.Equal(c.args, wantPytestArgs(junit, seed)) {
		t.Fatalf("pytest args\n%q\nwant\n%q\nD3/D4: no cache writes, every file collected despite one broken file, xunit1 for file= on every case, pytest-randomly and Hypothesis's plugin loaded explicitly and seeded from HEAD, and no file arguments so the whole unit suite runs", c.args, wantPytestArgs(junit, seed))
	}
	if junit == "" || strings.HasPrefix(junit, r.top+string(filepath.Separator)) {
		t.Fatalf("--junitxml=%q; the report goes to a temporary file outside the repository", junit)
	}
	if c.seed != seed {
		t.Fatalf("ASSURE_SEED=%q, want %q; adopter tests read the seed from the environment", c.seed, seed)
	}

	j := junitOf(t, out, resp)
	wantCases := map[string]ev.Outcome{
		"tests/test_cli.py.test_parse":            ev.Passed,
		"tests/test_cli.py.TestCli::test_neg[-1]": ev.Failed,
		"tests/test_broken.py.(collection)":       ev.Errored,
	}
	if got := caseOutcomes(j); !maps.Equal(got, wantCases) {
		t.Fatalf("cases %v, want %v", got, wantCases)
	}
	if text := failureText(j, "tests/test_broken.py.(collection)"); !strings.Contains(text, "No module named 'nope'") {
		t.Fatalf("collection case text %q; the spec requires the tool's output for the file that failed to collect", text)
	}
}

type reportKind string

var reportKinds = map[reportKind]pyReplay{
	"none":     {noReport: true},
	"garbage":  {report: "not xml"},
	"empty":    {report: xunit1()},
	"passing":  {report: xunit1(passCase)},
	"failing":  {report: xunit1(passCase, failCase)},
	"erroring": {report: xunit1(collectCase)},
}

// TestRunExitCodeIsClassified: D4 makes 0, 1, 2 (with a failing case or
// collection error in the report) and 5 results, everything else an adapter
// error. A misread exit code either reports a crashed pytest as a green
// suite or fails a green one.
func TestRunExitCodeIsClassified(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	writeTree(t, r.top, map[string]string{"app/parse.py": "changed = 1\n"})
	kinds := slices.Sorted(maps.Keys(reportKinds))
	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.SampledFrom(kinds).Draw(t, "report")
		code := rapid.OneOf(rapid.IntRange(0, 6), rapid.IntRange(7, 255)).Draw(t, "code")
		rp := reportKinds[kind]
		rp.code, rp.stdout = code, "PYTEST-OUTPUT-MARK"
		replayPytest(t, r.dataOf("."), rp)
		out, err := os.MkdirTemp(r.data, "out")
		if err != nil {
			t.Fatalf("%v", err)
		}
		readable := kind != "none" && kind != "garbage"
		failing := kind == "failing" || kind == "erroring"
		ok := code == 5 || (code <= 2 && readable && (code == 0 || failing))
		exit, raw, stderr := runPy("VER-TESTS-PASS", "HEAD", out)
		if !ok {
			if exit == 0 || len(raw) != 0 {
				t.Fatalf("pytest exit %d with %s report: run exited %d with %s; this outcome is not a test verdict and must fail the run, not pass as evidence", code, kind, exit, raw)
			}
			if !strings.Contains(stderr, "PYTEST-OUTPUT-MARK") || !strings.Contains(stderr, fmt.Sprint(code)) {
				t.Fatalf("stderr %q lacks pytest's output or exit code %d; the developer needs both to see why pytest stopped", stderr, code)
			}
			return
		}
		resp := runTestsIn(t, out)
		j := junitOf(t, out, resp)
		wantCases := 0
		if code != 5 {
			wantCases = strings.Count(rp.report, "<testcase")
		}
		if j.Cases != wantCases {
			t.Fatalf("pytest exit %d with %s report gave %d cases, want %d; exit 5 means nothing collected and is zero cases, not an error", code, kind, j.Cases, wantCases)
		}
	})
}

func TestRunTestsPerUnit(t *testing.T) {
	r := newPyRepo(t, map[string]string{"svc/a": "3.13.9", "svc/b": "3.12.4", "svc/c": "3.11.0"})
	writeTree(t, r.top, map[string]string{"svc/a/app/parse.py": "a = 1\n", "svc/b/tests/test_cli.py": "def test_parse():\n    assert 1\n"})
	for _, u := range []string{"svc/a", "svc/b"} {
		replayPytest(t, r.dataOf(u), pyReplay{report: xunit1(passCase), code: 0})
	}
	out := t.TempDir()
	resp := runTestsIn(t, out)
	for _, u := range []string{"svc/a", "svc/b"} {
		if c := pytestCall(t, r.dataOf(u)); c.cwd != filepath.Join(r.top, filepath.FromSlash(u)) {
			t.Fatalf("unit %s: pytest ran in %q; each unit runs from its own directory", u, c.cwd)
		}
	}
	if c := pytestCall(t, r.dataOf("svc/c")); c.invoked {
		t.Fatalf("svc/c has no changed file but pytest ran there with %q; only units with a selected file run", c.args)
	}
	if got := resp.ToolVersions["python"]; !strings.Contains(got, "3.13.9") || !strings.Contains(got, "3.12.4") || strings.Contains(got, "3.11.0") {
		t.Fatalf("tool_versions python %q; it must name every interpreter that ran tests and only those", got)
	}
	want := map[string]ev.Outcome{"svc/a/tests/test_cli.py.test_parse": ev.Passed, "svc/b/tests/test_cli.py.test_parse": ev.Passed}
	if got := caseOutcomes(junitOf(t, out, resp)); !maps.Equal(got, want) {
		t.Fatalf("cases %v, want %v; each unit's cases name files relative to the manifest root, so two units' same-named files stay distinct", got, want)
	}
}

// TestRunNoChangedPythonFiles is the spec scenario "No changed Python files
// gives empty test evidence" without a real interpreter.
func TestRunNoChangedPythonFiles(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	writeTree(t, r.top, map[string]string{"README.md": "changed\n"})
	out := t.TempDir()
	if j := junitOf(t, out, runTestsIn(t, out)); j.Cases != 0 {
		t.Fatalf("%d cases; no Python file changed, so the spec wants zero cases", j.Cases)
	}
	if c := pytestCall(t, r.dataOf(".")); c.invoked {
		t.Fatalf("pytest ran with %q though no Python file changed", c.args)
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{
		{"run"},
		{"run", "VER-TESTS-PASS"},
		{"run", "VER-TESTS-PASS", "--changed-from", "HEAD"},
		{"run", "VER-TESTS-PASS", "--out", "x"},
		{"run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", "x", "extra"},
		{"run", "VER-NO-SUCH", "--changed-from", "HEAD", "--out", "x"},
	} {
		var o, e bytes.Buffer
		if code := run(args, &o, &e); code != 2 || o.Len() != 0 {
			t.Fatalf("%q exited %d with stdout %q; a malformed or unknown run request is a usage error (exit 2) with no response for assure to read", args, code, o.String())
		}
	}
}

// TestRunNoChangedFilesReportsFirstUnitUnderRoot: with no changed Python
// file and no pyproject.toml at the manifest root, tool_versions still come
// from one real unit (the first pyproject.toml under the root by path), so
// the response passes the schema without inventing versions.
func TestRunNoChangedFilesReportsFirstUnitUnderRoot(t *testing.T) {
	r := newPyRepo(t, map[string]string{"svc/b": "3.12.4", "svc/a": "3.13.9"})
	writeTree(t, r.top, map[string]string{"NOTES.md": "changed\n"})
	out := t.TempDir()
	resp := runTestsIn(t, out)
	if got := resp.ToolVersions["python"]; got != "3.13.9" {
		t.Fatalf("tool_versions python %q, want svc/a's 3.13.9; the default unit is the first pyproject.toml under the root", got)
	}
	if j := junitOf(t, out, resp); j.Cases != 0 {
		t.Fatalf("%d cases; nothing Python changed", j.Cases)
	}
	for _, u := range []string{"svc/a", "svc/b"} {
		if c := pytestCall(t, r.dataOf(u)); c.invoked {
			t.Fatalf("pytest ran in %s with %q; the default unit only supplies tool versions", u, c.args)
		}
	}
}

func TestRunNoPyprojectUnderRootIsEnvironment(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{"README.md": "readme\n"})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	writeTree(t, top, map[string]string{"README.md": "changed\n"})
	t.Chdir(top)
	resp := runTestsIn(t, t.TempDir())
	if resp.Environment == nil || !strings.Contains(resp.Environment.Message, "pyproject.toml") || len(resp.Evidence) != 0 {
		t.Fatalf("response %+v; with no uv project under the manifest root there is no interpreter or tool to report, so the run is an environment problem naming pyproject.toml, not evidence", resp)
	}
}
