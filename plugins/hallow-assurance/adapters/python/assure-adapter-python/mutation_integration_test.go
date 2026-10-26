package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
	"pgregory.net/rapid"
)

const splitBase = `def parts_ok(parts):
    if parts > 2:
        return True
    return False


def untested(x):
    return x + 1


def total(a, b):
    return (a +
            b)


def zero():
    return 5
`

// splitChanged edits line 2 (>= has no test telling it from >), line 13
// (one line of a two-line expression) and line 17 (a literal 0).
var splitChanged = strings.NewReplacer("if parts > 2:", "if parts >= 2:", "            b)", "            b * 1)", "return 5", "return 0").Replace(splitBase)

const splitTests = `import os

from app.split import parts_ok, total, zero


def test_parts():
    assert os.environ["ASSURE_SEED"], "the seed reaches every mutant run"
    assert parts_ok(5)
    assert not parts_ok(0)


def test_total():
    assert total(2, 3) == 5


def test_zero():
    assert zero() == 0
`

var mutationRepoFiles = map[string]string{
	"app/split.py":        splitBase,
	"app/cold.py":         "def cold():\n    return 1\n",
	"tests/test_split.py": splitTests,
}

func readMutationReport(t *testing.T, out string, resp runResponse) strykerReport {
	t.Helper()
	if resp.Environment != nil {
		t.Fatalf("environment %q", resp.Environment.Message)
	}
	if want := []evidence{{Type: "mutation.report", Path: "mutation.json"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "mutation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.MutationReport, data); err != nil || len(vs) > 0 {
		t.Fatalf("mutation.json fails the vendored Stryker schema: %v %v", err, vs)
	}
	var rep strykerReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

func mutantsOnLine(f *strykerFile, line int) []strykerMutant {
	var out []strykerMutant
	for _, m := range f.Mutants {
		if m.Location.Start.Line == line {
			out = append(out, m)
		}
	}
	return out
}

// TestIntegrationMutationScenarios runs one mutation session over a tiny
// unit and checks the spec scenarios that a successful run decides.
func TestIntegrationMutationScenarios(t *testing.T) {
	top := realPyRepo(t, mutationRepoFiles)
	writeTree(t, top, map[string]string{"app/split.py": splitChanged, "app/cold.py": "def cold():\n    return 1 + 2\n", "app/fresh.py": "FRESH = 1 + 1\n"})
	before := repoState(t, top)
	out := t.TempDir()
	resp := runObjectiveIn(t, "VER-MUTATION-CHANGED", out)
	after := repoState(t, top)
	rep := readMutationReport(t, out, resp)
	split, cold := rep.Files["app/split.py"], rep.Files["app/cold.py"]

	t.Run("Repository untouched by mutation", func(t *testing.T) {
		if before != after {
			t.Fatalf("the run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
	t.Run("report shape", func(t *testing.T) {
		if names := slices.Sorted(maps.Keys(rep.Files)); !slices.Equal(names, []string{"app/cold.py", "app/split.py"}) {
			t.Fatalf("report files %q; the changed tracked source files, never the untracked app/fresh.py", names)
		}
		if split.Language != "python" || split.Source != splitChanged {
			t.Fatalf("app/split.py language %q, source equal %v; each file carries its current source", split.Language, split.Source == splitChanged)
		}
		for _, tool := range []string{"cosmic-ray", "parso", "pytest", "pytest-cov", "coverage"} {
			if resp.ToolVersions[tool] == "" {
				t.Fatalf("tool_versions %v lacks %s", resp.ToolVersions, tool)
			}
		}
	})
	checkSplitScenarios(t, split)
	checkUncoveredFile(t, cold)
}

func checkSplitScenarios(t *testing.T, split *strykerFile) {
	t.Run("Surviving mutant is reported", func(t *testing.T) {
		for _, m := range mutantsOnLine(split, 2) {
			if m.MutatorName == "ReplaceComparisonOperator_GtE_Gt" && m.Status == "Survived" && m.Replacement == ">" {
				return
			}
		}
		t.Fatalf("line 2 mutants %+v; no test tells >= from > there, so that mutant must survive", mutantsOnLine(split, 2))
	})
	t.Run("Unchanged lines are not mutated", func(t *testing.T) {
		for _, m := range split.Mutants {
			if !slices.Contains([]int{2, 13, 17}, m.Location.Start.Line) || m.Location.End.Line != m.Location.Start.Line {
				t.Fatalf("mutant %+v; only the changed lines 2, 13 and 17 may be mutated (untested() on line 8 included)", m)
			}
		}
	})
	t.Run("Mutant spanning an unchanged line is dropped", func(t *testing.T) {
		if ms := mutantsOnLine(split, 13); len(ms) == 0 {
			t.Fatalf("no mutant on changed line 13; the test cannot tell dropping from not generating")
		}
		if ms := mutantsOnLine(split, 12); len(ms) != 0 {
			t.Fatalf("mutants %+v start on unchanged line 12; `a +\\n b` spans lines 12-13 and only 13 changed", ms)
		}
	})
	t.Run("Two mutants on one span", func(t *testing.T) {
		ms := mutantsOnLine(split, 17)
		if len(ms) != 2 || ms[0].Location != ms[1].Location || ms[0].Replacement == ms[1].Replacement || ms[0].ID == ms[1].ID {
			t.Fatalf("line 17 mutants %+v; NumberReplacer gives 1 and -1 for the literal 0, with different ids", ms)
		}
	})
}

func checkUncoveredFile(t *testing.T, cold *strykerFile) {
	t.Run("Uncovered changed file", func(t *testing.T) {
		if len(cold.Mutants) == 0 {
			t.Fatal("app/cold.py has no mutants; `1 + 2` on its changed line must yield some")
		}
		for _, m := range cold.Mutants {
			if m.Status != "NoCoverage" {
				t.Fatalf("app/cold.py mutant %+v; no test imports it, so every mutant is NoCoverage", m)
			}
		}
	})
}

func TestIntegrationMutationNoChangedPythonFiles(t *testing.T) {
	top := realPyRepo(t, mutationRepoFiles)
	writeTree(t, top, map[string]string{"README.md": "changed\n"})
	out := t.TempDir()
	if rep := readMutationReport(t, out, runObjectiveIn(t, "VER-MUTATION-CHANGED", out)); len(rep.Files) != 0 {
		t.Fatalf("report files %v; only README.md changed", slices.Collect(maps.Keys(rep.Files)))
	}
}

// TestIntegrationMutationFailingBaselineFails is task 7.3: when the narrowed
// tests already fail, every mutant would read Killed, so the run errors,
// and the repository is still untouched ("successfully or not").
func TestIntegrationMutationFailingBaselineFails(t *testing.T) {
	top := realPyRepo(t, mutationRepoFiles)
	writeTree(t, top, map[string]string{
		"app/split.py":        strings.Replace(splitBase, "if parts > 2:", "if parts >= 2:", 1),
		"tests/test_split.py": splitTests + "\n\ndef test_two():\n    assert not parts_ok(2)\n",
	})
	before := repoState(t, top)
	code, raw, stderr := runPy("VER-MUTATION-CHANGED", "HEAD", t.TempDir())
	if code == 0 || len(raw) != 0 || !strings.Contains(stderr, "fail before mutation") {
		t.Fatalf("run exited %d with %s, stderr %q; a failing baseline must stop the run with pytest's output", code, raw, stderr)
	}
	if after := repoState(t, top); after != before {
		t.Fatalf("a failed run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

var guardLine = regexp.MustCompile(`app/bad\.py:3\b`)

// TestIntegrationMutationUnparseableSyntax is "Unparseable syntax fails
// closed" with PEP 758 `except A, B:`, which parso 0.8.7 reports as an
// error on every interpreter (CPython accepts it from 3.14).
func TestIntegrationMutationUnparseableSyntax(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("ASSURE_PYTEST_MARKER", marker)
	files := maps.Clone(mutationRepoFiles)
	files["conftest.py"] = "import os\n\n\ndef pytest_configure(config):\n    open(os.environ[\"ASSURE_PYTEST_MARKER\"], \"w\").close()\n"
	files["app/bad.py"] = "X = 1\n"
	top := realPyRepo(t, files)
	writeTree(t, top, map[string]string{"app/split.py": splitChanged, "app/bad.py": "try:\n    X = 1\nexcept ValueError, TypeError:\n    X = 2\n"})
	before := repoState(t, top)
	resp := runObjectiveIn(t, "VER-MUTATION-CHANGED", t.TempDir())
	if resp.Environment == nil || !guardLine.MatchString(resp.Environment.Message) || len(resp.Evidence) != 0 {
		t.Fatalf("response %+v; parso cannot read app/bad.py line 3, so the run reports environment naming file and line and writes no evidence", resp)
	}
	if exists(marker) {
		t.Fatal("pytest started; no mutant of any file may run once a changed file fails the syntax guard")
	}
	if after := repoState(t, top); after != before {
		t.Fatalf("the refused run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
	}
	t.Logf("fixture interpreter: python %s", venvPythonVersion(t))
}

// TestSyntaxGuardDecodesEveryEncodingAndNewline: the guard decodes each file
// with its PEP 263 encoding whatever its newlines, in the oldest supported
// interpreter, so its verdict matches the same text as plain UTF-8 with "\n"
// newlines. The comparison is differential because parso rejects some
// modules CPython accepts (form feeds in indentation), and reporting those is
// the guard's job. A file it cannot decode must not crash the batch: that
// turns every VER-MUTATION-CHANGED run touching it into an environment
// failure.
func TestSyntaxGuardDecodesEveryEncodingAndNewline(t *testing.T) {
	py := filepath.Join(syncedProject(t), ".venv", "bin", "python")
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp("", "assure-py-syntax-*")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		m, src := genModule(t)
		plain := slices.Clone(m.lines)
		if len(plain) > 0 && strings.HasPrefix(plain[0], "# -*- coding: latin-1") {
			plain[0] = "# -*- coding: utf-8 -*-"
		}
		lead := rapid.IntRange(0, 3).Draw(t, "lead")
		enc, ref, bad := filepath.Join(dir, "enc.py"), filepath.Join(dir, "ref.py"), filepath.Join(dir, "bad.py")
		writeTree(t, dir, map[string]string{
			"enc.py": string(src),
			"ref.py": strings.Join(append(plain, ""), "\n"),
			"bad.py": strings.Repeat("x = 1\n", lead) + "def broken(:\n",
		})
		got, err := syntaxGuard(py, dir, []string{enc, ref, bad})
		if err != nil {
			t.Fatalf("syntaxGuard: %v\nsource %q\none undecodable file must not fail the whole batch", err, src)
		}
		var encVerdict, refVerdict string
		for line := range strings.Lines(got) {
			if r, ok := strings.CutPrefix(line, enc); ok {
				encVerdict = strings.TrimSpace(r)
			}
			if r, ok := strings.CutPrefix(line, ref); ok {
				refVerdict = strings.TrimSpace(r)
			}
		}
		if encVerdict != refVerdict {
			t.Fatalf("verdict %q for the encoded source, %q for the same text as UTF-8 with \"\\n\" newlines\nsource %q\nparso must read the source decoded with its coding cookie, whatever its newlines", encVerdict, refVerdict, src)
		}
		if want := fmt.Sprintf("%s:%d:", bad, lead+1); !strings.Contains(got, want) {
			t.Fatalf("guard output %q, want %s; an unparseable file must be named with its line so the run stops before any mutant", got, want)
		}
	})
}

func TestMutationToolNotInstalled(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	site := filepath.Join(r.top, ".venv", "lib", "python3.13", "site-packages")
	if err := os.Rename(filepath.Join(site, uvDistDir("cosmic-ray", specRanges["cosmic-ray"][0])), filepath.Join(r.data, "aside")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, r.top, map[string]string{"app/parse.py": "def parse(s):\n    return int(s) + 0\n"})
	resp := runObjectiveIn(t, "VER-MUTATION-CHANGED", t.TempDir())
	if resp.Environment == nil || !strings.Contains(resp.Environment.Message, "cosmic-ray") || !strings.Contains(resp.Environment.Message, "uv sync --locked") || len(resp.Evidence) != 0 {
		t.Fatalf("response %+v; without cosmic-ray the run reports environment naming it and the install line, and runs nothing", resp)
	}
	if c := pytestCall(t, r.dataOf(".")); c.invoked {
		t.Fatalf("pytest ran with %q though a required tool is missing", c.args)
	}
}

var (
	arithmeticD8 = []string{"Add", "Sub", "Mul", "Div", "FloorDiv", "Mod"}
	bitwiseD8    = []string{"LShift", "RShift", "BitOr", "BitAnd", "BitXor"}
	orderingD8   = []string{"Lt", "LtE", "Gt", "GtE"}
	identityD8   = []string{"Is", "IsNot"}
)

// d8Keeps is D8 read directly: binary replacements stay within the
// arithmetic or the shift/bitwise family (Pow in neither), ordering and
// identity comparisons do not swap, and the exception, decorator and two
// random-valued variable operators are dropped.
func d8Keeps(op string) bool {
	name := strings.TrimPrefix(op, "core/")
	if rest, ok := strings.CutPrefix(name, "ReplaceBinaryOperator_"); ok {
		a, b, _ := strings.Cut(rest, "_")
		return (slices.Contains(arithmeticD8, a) && slices.Contains(arithmeticD8, b)) || (slices.Contains(bitwiseD8, a) && slices.Contains(bitwiseD8, b))
	}
	if rest, ok := strings.CutPrefix(name, "ReplaceComparisonOperator_"); ok {
		a, b, _ := strings.Cut(rest, "_")
		cross := (slices.Contains(orderingD8, a) && slices.Contains(identityD8, b)) || (slices.Contains(identityD8, a) && slices.Contains(orderingD8, b))
		return !cross
	}
	return !slices.Contains([]string{"ExceptionReplacer", "RemoveDecorator", "VariableReplacer", "VariableInserter"}, name)
}

// TestExcludedOperatorsMatchD8 applies excludedOperators() the way
// cr-filter-operators does (re.match over the joined patterns) to the
// installed cosmic-ray's own operator list.
func TestExcludedOperatorsMatchD8(t *testing.T) {
	proj := syncedProject(t)
	stdout, stderr, code, err := runToolEnv(proj, nil, filepath.Join(proj, ".venv", "bin", "cosmic-ray"), "operators")
	if err != nil || code != 0 {
		t.Fatalf("cosmic-ray operators: %v %s", err, stderr)
	}
	var parts []string
	for _, e := range excludedOperators() {
		parts = append(parts, "(?:"+e+")")
	}
	exclude := regexp.MustCompile("^(?:" + strings.Join(parts, "|") + ")")
	ops := strings.Fields(string(stdout))
	if len(ops) < 100 {
		t.Fatalf("cosmic-ray listed %d operators: %s", len(ops), stdout)
	}
	for _, op := range ops {
		if kept := !exclude.MatchString(op); kept != d8Keeps(op) {
			t.Fatalf("%s kept=%v, D8 says %v", op, kept, d8Keeps(op))
		}
	}
}

// TestIntegrationMutationUnitWithoutTargetsDoesNotStopOthers: a unit whose
// change touches only tests has nothing to mutate, and must not end the
// listing of later units; otherwise a source change there gets no mutant and
// VER-MUTATION-CHANGED passes vacuously.
func TestIntegrationMutationUnitWithoutTargetsDoesNotStopOthers(t *testing.T) {
	proj := syncedProject(t)
	pyproject, err := os.ReadFile(filepath.Join(proj, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	top := realPyRepo(t, map[string]string{
		"svc/a/pyproject.toml":  string(pyproject),
		"svc/a/tests/test_a.py": "def test_a():\n    assert True\n",
		"svc/b/pyproject.toml":  string(pyproject),
		"svc/b/app/__init__.py": "",
		"svc/b/app/m.py":        "def f(x):\n    return x\n",
		"svc/b/tests/test_m.py": "from app.m import f\n\n\ndef test_f():\n    assert f(1) in (1, 2)\n",
	})
	writeTree(t, top, map[string]string{"svc/a/tests/test_a.py": "def test_a():\n    assert 1\n", "svc/b/app/m.py": "def f(x):\n    return x + 1\n"})
	out := t.TempDir()
	rep := readMutationReport(t, out, runObjectiveIn(t, "VER-MUTATION-CHANGED", out))
	if f := rep.Files["svc/b/app/m.py"]; f == nil || len(f.Mutants) == 0 {
		t.Fatalf("report files %v; svc/b/app/m.py changed line 2, so it must be mutated though svc/a (sorted first) has only a test change", slices.Collect(maps.Keys(rep.Files)))
	}
}

// TestIntegrationMutationEditableInstallDoesNotLeak: uv installs the
// project editable, so without the tree's src/ ahead of it every mutant
// would run the repository's unmutated code and survive. With the tree on
// the path, mutants of the changed comparison are killed.
func TestIntegrationMutationEditableInstallDoesNotLeak(t *testing.T) {
	top := fobRepo(t)
	writeTree(t, top, map[string]string{
		"src/demo/rng.py":    "def in_range(x, lo, hi):\n    return lo <= x <= hi\n",
		"tests/test_kill.py": "from demo.rng import in_range\n\n\ndef test_bounds():\n    assert in_range(0, 0, 2) and in_range(2, 0, 2)\n    assert not in_range(-1, 0, 2) and not in_range(3, 0, 2)\n",
	})
	out := t.TempDir()
	rep := readMutationReport(t, out, runObjectiveIn(t, "VER-MUTATION-CHANGED", out))
	f := rep.Files["src/demo/rng.py"]
	if f == nil {
		t.Fatalf("report files %v; src/demo/rng.py changed", slices.Collect(maps.Keys(rep.Files)))
	}
	killed := 0
	for _, m := range f.Mutants {
		if m.Status == "Killed" {
			killed++
		}
	}
	if killed == 0 {
		t.Fatalf("mutants %+v; test_bounds pins both bounds, so mutants of `lo <= x <= hi` die unless the tests import the repository's code through the editable install", f.Mutants)
	}
}
