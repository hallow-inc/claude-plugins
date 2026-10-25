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
	"time"

	"pgregory.net/rapid"
)

func TestHunkRangesMatchesModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var diff strings.Builder
		diff.WriteString("diff --git a/x.py b/x.py\n--- a/x.py\n+++ b/x.py\n")
		var want []lineRange
		for range rapid.IntRange(0, 4).Draw(t, "hunks") {
			start, count := rapid.IntRange(1, 500).Draw(t, "start"), rapid.IntRange(0, 4).Draw(t, "count")
			plus := fmt.Sprintf("+%d,%d", start, count)
			if count == 1 && rapid.Bool().Draw(t, "short") {
				plus = fmt.Sprintf("+%d", start)
			}
			fmt.Fprintf(&diff, "@@ -%d,%d %s @@ def f():\n-old @@ -9 +9 @@\n+new\n", start, rapid.IntRange(0, 3).Draw(t, "old"), plus)
			if count > 0 {
				want = append(want, lineRange{start, start + count - 1})
			}
		}
		got, err := hunkRanges([]byte(diff.String()))
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("hunkRanges = %v, %v; want %v\n%s\nthe changed lines are the new-side ranges of git diff -U0; a pure deletion (count 0) changes no line that remains, and only lines starting with @@ are headers", got, err, want, diff.String())
		}
	})
}

var (
	workerOutcomes = []string{"normal", "normal", "normal", "exception", "abnormal", "no-test", "skipped"}
	testOutcomes   = []string{"", "survived", "killed", "incompetent"}
	sentinelValues = []string{"0", "1", "2", "3", "4", "5", "137"}
	noiseLines     = []string{"F.", "1 failed in 0.1s", "xASSURE_PYTEST_EXIT=1", "ASSURE_PYTEST_EXIT=1 extra", "ASSURE_PYTEST_EXIT=", "timeout"}
)

// mutantStatusModel is D7 step 6: any worker outcome but normal is an
// error; cosmic-ray's timeout result is Timeout; otherwise the last
// sentinel line decides, 0 Survived, 1 or 2 Killed, anything else (a usage
// error, an internal error, nothing collected, no sentinel) an error.
func mutantStatusModel(worker, test, output string, sentinels []string) (string, bool) {
	if worker != "normal" {
		return "", false
	}
	if test == "killed" && output == "timeout" {
		return "Timeout", true
	}
	if len(sentinels) == 0 {
		return "", false
	}
	switch sentinels[len(sentinels)-1] {
	case "0":
		return "Survived", true
	case "1", "2":
		return "Killed", true
	}
	return "", false
}

// TestMutantStatusMatchesSpec: cosmic-ray scores every non-zero exit as
// killed, so a pytest usage error, an internal error or "no tests" would
// otherwise count as a detected mutant and inflate the score.
func TestMutantStatusMatchesSpec(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		worker := rapid.SampledFrom(workerOutcomes).Draw(t, "worker")
		test := rapid.SampledFrom(testOutcomes).Draw(t, "test")
		var output string
		var sentinels []string
		if rapid.IntRange(0, 4).Draw(t, "timeoutShape") == 0 {
			output = "timeout"
		} else {
			var lines []string
			for range rapid.IntRange(0, 4).Draw(t, "lines") {
				if rapid.Bool().Draw(t, "sentinel") {
					v := rapid.SampledFrom(sentinelValues).Draw(t, "value")
					sentinels = append(sentinels, v)
					lines = append(lines, exitSentinel+v+rapid.SampledFrom([]string{"", " ", "\r"}).Draw(t, "pad"))
				} else {
					lines = append(lines, rapid.SampledFrom(noiseLines).Draw(t, "noise"))
				}
			}
			output = strings.Join(lines, "\n") + rapid.SampledFrom([]string{"", "\n"}).Draw(t, "nl")
		}
		r := cosmicResult{WorkerOutcome: worker, Output: output}
		if test != "" {
			r.TestOutcome = &test
		}
		got, err := mutantStatus(r)
		want, ok := mutantStatusModel(worker, test, output, sentinels)
		if ok != (err == nil) || got != want {
			t.Fatalf("mutantStatus(worker %q, test %q, output %q) = %q, %v; want %q (ok %v)", worker, test, output, got, err, want, ok)
		}
		if got == "Killed" && !slices.Contains([]string{"1", "2"}, sentinels[len(sentinels)-1]) {
			t.Fatalf("Killed from sentinel %v; only pytest's own failure codes 1 and 2 detect a mutant", sentinels)
		}
	})
}

// cosmicDiff asks the real difflib, as cosmic-ray's _make_diff does, for
// the mutation diffs of a batch of span edits; columns are code points.
const cosmicDiffScript = `import difflib, json, sys
out = []
for c in json.load(sys.stdin):
    src = c["src"]
    lines = src.split("\n")
    def off(pos):
        return sum(len(l) + 1 for l in lines[:pos[0] - 1]) + pos[1]
    mutated = src[:off(c["start"])] + c["repl"] + src[off(c["end"]):]
    diff = ["--- mutation diff ---"] + list(difflib.unified_diff(src.split("\n"), mutated.split("\n"), fromfile="a/m.py", tofile="b/m.py", lineterm=""))
    out.append({"mutated": mutated, "diff": "\n".join(diff)})
json.dump(out, sys.stdout)
`

type spanEdit struct {
	Src   string `json:"src"`
	Start [2]int `json:"start"`
	End   [2]int `json:"end"`
	Repl  string `json:"repl"`
}

var (
	srcLineGen  = rapid.SampledFrom([]string{"", "x = 1", "    return a >= 2", "s = 'é€😀'", "\tif x:", "y = a + b", "-", "+", " ", "--- x", "@@ -1 +1 @@"})
	replTextGen = rapid.SampledFrom([]string{"", "1", "-1", "not ", "é", ">", "a\nb", "😀😀"})
)

func drawSpanEdit(t *rapid.T) spanEdit {
	lines := rapid.SliceOfN(srcLineGen, 1, 12).Draw(t, "lines")
	src := strings.Join(lines, "\n") + rapid.SampledFrom([]string{"", "\n"}).Draw(t, "trailing")
	all := strings.Split(src, "\n")
	pos := func(label string) [2]int {
		l := rapid.IntRange(1, len(all)).Draw(t, label+"Line")
		return [2]int{l, rapid.IntRange(0, len([]rune(all[l-1]))).Draw(t, label+"Col")}
	}
	a, b := pos("a"), pos("b")
	if b[0] < a[0] || (b[0] == a[0] && b[1] < a[1]) {
		a, b = b, a
	}
	return spanEdit{Src: src, Start: a, End: b, Repl: replTextGen.Draw(t, "repl")}
}

// TestApplyDiffRecoversReplacement: the replacement is the mutated text of
// the span, rebuilt from cosmic-ray's split("\n") diff, with code-point
// columns; anything else and two NumberReplacer twins (or a mutant on a
// line with é) get the wrong text and the wrong id.
func TestApplyDiffRecoversReplacement(t *testing.T) {
	py := testPython(t)
	rapid.Check(t, func(t *rapid.T) {
		e := drawSpanEdit(t)
		in, err := json.Marshal([]spanEdit{e})
		if err != nil {
			t.Fatalf("%v", err)
		}
		out := pythonJSON(t, py, cosmicDiffScript, in)
		got, err := applyDiff(e.Src, out[0].Diff)
		if err != nil || got != out[0].Mutated {
			t.Fatalf("applyDiff = %q, %v; want %q\nsource %q\ndiff %q", got, err, out[0].Mutated, e.Src, out[0].Diff)
		}
		repl, err := replacementText(e.Src, got, e.Start, e.End)
		if err != nil || repl != e.Repl {
			t.Fatalf("replacementText(%v-%v) = %q, %v; want %q\nsource %q", e.Start, e.End, repl, err, e.Repl, e.Src)
		}
	})
}

type diffOut struct {
	Mutated string `json:"mutated"`
	Diff    string `json:"diff"`
}

func pythonJSON(t *rapid.T, py, script string, in []byte) []diffOut {
	dir, err := os.MkdirTemp("", "assure-py-diff-*")
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	inFile := filepath.Join(dir, "in.json")
	if err := os.WriteFile(inFile, in, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	wrapped := "import sys\nsys.stdin = open(sys.argv[1], encoding='utf-8')\n" + script
	stdout, stderr, code, err := runToolEnv(dir, nil, py, "-I", "-c", wrapped, inFile)
	if err != nil || code != 0 {
		t.Fatalf("python: exit %d %v\n%s", code, err, stderr)
	}
	var out []diffOut
	if err := json.Unmarshal(stdout, &out); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	return out
}

// TestReplacementTextRejectsOutsideEdits: a diff that also changes text
// outside the span means the positions and the diff disagree, so the
// replacement (and id) cannot be trusted.
func TestReplacementTextRejectsOutsideEdits(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := drawSpanEdit(t)
		runes := []rune(e.Src)
		lines := strings.Split(e.Src, "\n")
		off := func(p [2]int) int {
			n := 0
			for _, l := range lines[:p[0]-1] {
				n += len([]rune(l)) + 1
			}
			return n + p[1]
		}
		so, eo := off(e.Start), off(e.End)
		var outside []int
		for i := range runes {
			if i < so || i >= eo {
				outside = append(outside, i)
			}
		}
		if len(outside) == 0 {
			t.Skip("span covers the whole file")
		}
		i := rapid.SampledFrom(outside).Draw(t, "outside")
		mutated := string(runes[:so]) + e.Repl + string(runes[eo:])
		shift := len([]rune(e.Repl)) - (eo - so)
		j := i
		if i >= eo {
			j = i + shift
		}
		mr := []rune(mutated)
		mr[j] = 'Z'
		if runes[i] == 'Z' {
			mr[j] = 'Y'
		}
		if r, err := replacementText(e.Src, string(mr), e.Start, e.End); err == nil {
			t.Fatalf("replacementText accepted a mutation that also changed rune %d outside %v-%v (got %q)", i, e.Start, e.End, r)
		}
	})
}

var (
	mutFileGen = rapid.SampledFrom([]string{"app/a.py", "app/b.py"})
	mutOpGen   = rapid.SampledFrom([]string{"NumberReplacer", "ReplaceComparisonOperator_GtE_Gt"})
	mutRepGen  = rapid.SampledFrom([]string{"1", "-1", ">"})
	mutStatGen = rapid.SampledFrom([]string{"Killed", "Survived", "NoCoverage", "Timeout"})
)

func drawMutant(t *rapid.T) (string, strykerMutant) {
	var m strykerMutant
	m.MutatorName, m.Replacement, m.Status = mutOpGen.Draw(t, "op"), mutRepGen.Draw(t, "rep"), mutStatGen.Draw(t, "status")
	m.Location.Start = strykerPosition{rapid.IntRange(1, 2).Draw(t, "sl"), rapid.IntRange(1, 2).Draw(t, "sc")}
	m.Location.End = strykerPosition{rapid.IntRange(1, 2).Draw(t, "el"), rapid.IntRange(1, 2).Draw(t, "ec")}
	return mutFileGen.Draw(t, "file"), m
}

// TestMutantIDDependsOnlyOnIdentity: ids must match across runs and
// commits for the same mutant (the core diffs mutants by id) and differ for
// different ones; the status must not enter it.
func TestMutantIDDependsOnlyOnIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		fa, a := drawMutant(t)
		fb, b := drawMutant(t)
		same := fa == fb && a.MutatorName == b.MutatorName && a.Replacement == b.Replacement && a.Location == b.Location
		if ida, idb := mutantID(fa, a), mutantID(fb, b); (ida == idb) != same {
			t.Fatalf("ids %s %s for %s %+v and %s %+v; same identity (file, position, operator, replacement) must mean same id and nothing else", ida, idb, fa, a, fb, b)
		}
	})
}

func TestUniqueIDsRejectsDuplicates(t *testing.T) {
	rep := newStrykerReport()
	rep.Files["app/a.py"] = &strykerFile{Mutants: []strykerMutant{{ID: "x"}, {ID: "y"}}}
	rep.Files["app/b.py"] = &strykerFile{Mutants: []strykerMutant{{ID: "z"}}}
	if err := uniqueIDs(rep); err != nil {
		t.Fatalf("uniqueIDs on distinct ids: %v", err)
	}
	rep.Files["app/b.py"].Mutants = append(rep.Files["app/b.py"].Mutants, strykerMutant{ID: "x"})
	if err := uniqueIDs(rep); err == nil {
		t.Fatal("uniqueIDs accepted two mutants with one id; the spec makes that an error, since the core would merge their results")
	}
}

// selectionModel is the spec's rule: the tests that executed any changed
// line; the whole suite when a changed line was executed outside any test;
// NoCoverage when no changed line was executed at all.
func selectionModel(f contextFile, ranges []lineRange) ([]string, bool, bool) {
	set := map[string]bool{}
	whole, covered := false, false
	for line, ctxs := range f.Contexts {
		var n int
		if _, err := fmt.Sscan(line, &n); err != nil || !inRanges(ranges, n) {
			continue
		}
		for _, c := range ctxs {
			covered = true
			id, _, _ := strings.Cut(c, "|")
			if id == "" {
				whole = true
			} else {
				set[id] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set)), whole, covered
}

var ctxGen = rapid.SampledFrom([]string{"tests/test_a.py::test_x|run", "tests/test_a.py::test_x|setup", "tests/test_b.py::TestK::test_y[1]|run", "tests/test_b.py::TestK::test_y[1]|teardown", "tests/test_c.py::test_z|run"})

// TestTestSelectionMatchesModel draws recorded-style coverage contexts in
// which a changed line is run by tests or only at import time; lines run
// both ways are left to TestImportAndTestExecutedLineWidens.
func TestTestSelectionMatchesModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := contextFile{Contexts: map[string][]string{}}
		for line := 1; line <= 8; line++ {
			switch rapid.SampledFrom([]string{"none", "tests", "import"}).Draw(t, "kind") {
			case "tests":
				f.Contexts[fmt.Sprint(line)] = rapid.SliceOfN(ctxGen, 1, 3).Draw(t, "ctxs")
			case "import":
				f.Contexts[fmt.Sprint(line)] = []string{""}
			}
		}
		var ranges []lineRange
		for range rapid.IntRange(0, 2).Draw(t, "ranges") {
			s := rapid.IntRange(1, 8).Draw(t, "s")
			ranges = append(ranges, lineRange{s, s + rapid.IntRange(0, 2).Draw(t, "len")})
		}
		ids, whole, covered := testSelection(f, ranges)
		wids, wwhole, wcovered := selectionModel(f, ranges)
		if !slices.Equal(ids, wids) || whole != wwhole || covered != wcovered {
			t.Fatalf("testSelection = %q whole %v covered %v; want %q %v %v\ncontexts %v ranges %v", ids, whole, covered, wids, wwhole, wcovered, f.Contexts, ranges)
		}
	})
}

// TestImportAndTestExecutedLineWidens is the spec text "when a changed line
// was executed outside any test (for example at import time), its file's
// mutants SHALL run against every test in the unit" for a line that also
// ran inside a test: a mutant there changes import-time behaviour that any
// test importing the module may observe.
func TestImportAndTestExecutedLineWidens(t *testing.T) {
	f := contextFile{Contexts: map[string][]string{"3": {"", "tests/test_a.py::test_x|run"}}}
	if _, whole, covered := testSelection(f, []lineRange{{3, 3}}); !whole || !covered {
		t.Fatalf("whole %v covered %v; line 3 ran at import time as well as in test_x, so the spec runs the whole suite", whole, covered)
	}
}

// TestTestScriptReportsPytestExit runs the generated script with a stand-in
// interpreter: it must pass pytest -x without cache writes, the seed flags
// and the @argsfile, and print pytest's own exit code as the last line, with
// paths that need shell quoting.
func TestTestScriptReportsPytestExit(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		dir := filepath.Join(tempIn(t, base), rapid.SampledFrom([]string{"plain", "with space", "it's", "é"}).Draw(t, "dir"))
		code := rapid.IntRange(0, 5).Draw(t, "code")
		py := filepath.Join(dir, "python")
		exe(t, py, fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\necho pytest output\nexit %d\n", shellQuote(filepath.Join(dir, "args")), code))
		argsFile := ""
		if rapid.Bool().Draw(t, "args") {
			argsFile = filepath.Join(dir, "tests.args")
		}
		script := filepath.Join(dir, "tests.sh")
		writeTree(t, dir, map[string]string{"tests.sh": testScript(py, "42", argsFile)})
		stdout, stderr, got, err := runToolEnv(dir, nil, "sh", script)
		if err != nil || got != code {
			t.Fatalf("script exited %d (%v), want pytest's %d: %s", got, err, code, stderr)
		}
		lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
		if last := lines[len(lines)-1]; last != exitSentinel+fmt.Sprint(code) {
			t.Fatalf("last line %q, want %s%d; the adapter reads the exit code from this line", last, exitSentinel, code)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "args"))
		if err != nil {
			t.Fatalf("%v", err)
		}
		want := append([]string{"-m", "pytest", "-x", "-q", "-p", "no:cacheprovider"}, seedArgs("42")...)
		if argsFile != "" {
			want = append(want, "@"+argsFile)
		}
		if args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"); !slices.Equal(args, want) {
			t.Fatalf("pytest args %q, want %q; -x stops at the first failure, the seed is set, and @argsfile narrows to the tests that ran the changed lines", args, want)
		}
	})
}

func TestTestScriptCarriesPytestStderrOnStdout(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		dir := tempIn(t, base)
		code := rapid.IntRange(0, 5).Draw(t, "code")
		lines := rapid.SliceOfN(rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9 :'"()./-]{0,40}`), 1, 4).Draw(t, "stderr lines")
		writeTree(t, dir, map[string]string{"stderr.txt": strings.Join(lines, "\n") + "\n"})
		py := filepath.Join(dir, "python")
		exe(t, py, fmt.Sprintf("#!/bin/sh\ncat %s >&2\nexit %d\n", shellQuote(filepath.Join(dir, "stderr.txt")), code))
		script := filepath.Join(dir, "tests.sh")
		writeTree(t, dir, map[string]string{"tests.sh": testScript(py, "42", "")})
		stdout, _, got, err := runToolEnv(dir, nil, "sh", script)
		if err != nil || got != code {
			t.Fatalf("script exited %d (%v), want pytest's %d", got, err, code)
		}
		out := strings.Split(strings.TrimSpace(string(stdout)), "\n")
		if last := out[len(out)-1]; last != exitSentinel+fmt.Sprint(code) {
			t.Fatalf("last line %q, want %s%d; the adapter reads the exit code from this line", last, exitSentinel, code)
		}
		if !slices.Equal(out[:len(out)-1], lines) {
			t.Fatalf("stdout before the exit line is %q, want pytest's stderr %q: cosmic-ray keeps only stdout, so a usage error written to stderr would reach the run's error without the tool's output", out[:len(out)-1], lines)
		}
	})
}

const tomlCheckScript = `import json, sys, tomllib
with open(sys.argv[1], "rb") as f:
    c = tomllib.load(f)
cr = c["cosmic-ray"]
json.dump({"module": cr["module-path"], "timeout": cr["timeout"], "command": cr["test-command"], "distributor": cr["distributor"]["name"],
           "exclude": cr["filters"]["operators-filter"]["exclude-operators"], "lines": cr["filters"]["line-filter"]["lines"]}, sys.stdout)
`

// TestCosmicConfigIsValidTOML: module paths and commands with quotes,
// backslashes or non-ASCII must survive into cosmic-ray's TOML exactly, or
// cosmic-ray mutates the wrong file or runs the wrong command.
func TestCosmicConfigIsValidTOML(t *testing.T) {
	py := testPython(t)
	base := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		module := rapid.SampledFrom([]string{"app/parse.py", "src/é/x.py", `we"ird/a.py`, `b\s/c.py`}).Draw(t, "module")
		command := "sh " + shellQuote(rapid.SampledFrom([]string{"/tmp/a b/tests.sh", `/tmp/it's"/t.sh`}).Draw(t, "script"))
		timeout := time.Duration(rapid.IntRange(60, 600).Draw(t, "seconds")) * time.Second
		var ranges []lineRange
		var want []string
		for range rapid.IntRange(1, 3).Draw(t, "ranges") {
			s := rapid.IntRange(1, 99).Draw(t, "s")
			r := lineRange{s, s + rapid.IntRange(0, 3).Draw(t, "n")}
			ranges = append(ranges, r)
			want = append(want, fmt.Sprintf("%d-%d", r.start, r.end))
		}
		cfg := filepath.Join(tempIn(t, base), "cr.toml")
		if err := os.WriteFile(cfg, []byte(cosmicConfig(module, command, timeout, ranges)), 0o644); err != nil {
			t.Fatalf("%v", err)
		}
		stdout, stderr, code, err := runToolEnv(base, nil, py, "-I", "-c", tomlCheckScript, cfg)
		if err != nil || code != 0 {
			t.Fatalf("tomllib rejects the config: %v %s", err, stderr)
		}
		var got struct {
			Module, Command, Distributor string
			Timeout                      float64
			Exclude                      []string
			Lines                        map[string][]string
		}
		if err := json.Unmarshal(stdout, &got); err != nil {
			t.Fatalf("%v", err)
		}
		if got.Module != module || got.Command != command || got.Distributor != "local" || got.Timeout != timeout.Seconds() ||
			!slices.Equal(got.Exclude, excludedOperators()) || !maps.EqualFunc(got.Lines, map[string][]string{module: want}, slices.Equal) {
			t.Fatalf("config reads back as %+v; want module %q, command %q, local distributor, timeout %v, the D8 exclusions, and lines %q for the module", got, module, command, timeout.Seconds(), want)
		}
	})
}

// recordedNumberReplacer is the two dump lines cosmic-ray 8.7.0 wrote for
// "    return 0  # é" (line 2), outputs shortened and the sentinel added.
const recordedNumberReplacer = `[{"job_id": "1d86", "mutations": [{"module_path": "mod.py", "operator_name": "core/NumberReplacer", "occurrence": 0, "start_pos": [2, 11], "end_pos": [2, 12], "operator_args": {}, "definition_name": "zero"}]}, {"worker_outcome": "normal", "output": "F\n1 failed in 0.12s\nASSURE_PYTEST_EXIT=1\n", "test_outcome": "killed", "diff": "--- mutation diff ---\n--- a/mod.py\n+++ b/mod.py\n@@ -1,3 +1,3 @@\n def zero():\n-    return 0  # é\n+    return 1  # é\n "}]
[{"job_id": "038c", "mutations": [{"module_path": "mod.py", "operator_name": "core/NumberReplacer", "occurrence": 1, "start_pos": [2, 11], "end_pos": [2, 12], "operator_args": {}, "definition_name": "zero"}]}, {"worker_outcome": "normal", "output": "F\n1 failed in 0.09s\nASSURE_PYTEST_EXIT=1\n", "test_outcome": "killed", "diff": "--- mutation diff ---\n--- a/mod.py\n+++ b/mod.py\n@@ -1,3 +1,3 @@\n def zero():\n-    return 0  # é\n+    return -1  # é\n "}]
`

const recordedSource = "def zero():\n    return 0  # é\n"

// TestTwoMutantsOnOneSpanFromRecordedDump is spec scenario "Two mutants on
// one span" from a recorded NumberReplacer dump.
func TestTwoMutantsOnOneSpanFromRecordedDump(t *testing.T) {
	muts, err := mutantsFromDump("app/mod.py", recordedSource, []byte(recordedNumberReplacer), []lineRange{{2, 2}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(muts) != 2 || muts[0].Replacement == muts[1].Replacement || muts[0].ID == muts[1].ID {
		t.Fatalf("mutants %+v; NumberReplacer's two mutants of one literal need different replacements (1, -1) and different ids", muts)
	}
	for _, m := range muts {
		if m.Location.Start != (strykerPosition{2, 12}) || m.Location.End != (strykerPosition{2, 13}) || m.Status != "Killed" || m.MutatorName != "NumberReplacer" {
			t.Fatalf("mutant %+v; want 2:12-2:13 (Stryker columns are cosmic-ray's 0-based ones plus 1), Killed, NumberReplacer", m)
		}
	}
	if reps := []string{muts[0].Replacement, muts[1].Replacement}; !slices.Contains(reps, "1") || !slices.Contains(reps, "-1") {
		t.Fatalf("replacements %q, want 1 and -1", reps)
	}
	checkRecordedDumpFilters(t)
}

func checkRecordedDumpFilters(t *testing.T) {
	uncovered, err := mutantsFromDump("app/mod.py", recordedSource, []byte(recordedNumberReplacer), []lineRange{{2, 2}}, false)
	if err != nil || len(uncovered) != 2 || uncovered[0].Status != "NoCoverage" || uncovered[1].Status != "NoCoverage" {
		t.Fatalf("uncovered = %+v, %v; a file no test executes reports NoCoverage whatever its run printed", uncovered, err)
	}
	if none, err := mutantsFromDump("app/mod.py", recordedSource, []byte(recordedNumberReplacer), []lineRange{{1, 1}}, true); err != nil || len(none) != 0 {
		t.Fatalf("mutants on unchanged line 2 = %+v, %v; want none", none, err)
	}
}

var dumpMutation = regexp.MustCompile(`"start_pos": \[2, 11\], "end_pos": \[2, 12\]`)

// TestMutantsFromDumpSpanFilter: a mutant is kept only when every line of
// its span changed (an end at column 0 ends on the line before); skipped
// jobs are dropped, and a job without a diff is an error, never a guess.
func TestMutantsFromDumpSpanFilter(t *testing.T) {
	first, _, _ := strings.Cut(recordedNumberReplacer, "\n")
	cases := []struct {
		span   string
		ranges []lineRange
		keep   bool
	}{
		{`"start_pos": [1, 4], "end_pos": [2, 12]`, []lineRange{{2, 2}}, false},
		{`"start_pos": [1, 4], "end_pos": [2, 12]`, []lineRange{{1, 2}}, true},
		{`"start_pos": [2, 11], "end_pos": [3, 0]`, []lineRange{{2, 2}}, true},
		{`"start_pos": [2, 11], "end_pos": [3, 1]`, []lineRange{{2, 2}}, false},
		{`"start_pos": [2, 0], "end_pos": [2, 0]`, []lineRange{{1, 1}}, false},
	}
	for _, c := range cases {
		line := dumpMutation.ReplaceAllString(first, c.span)
		muts, err := mutantsFromDump("app/mod.py", recordedSource, []byte(line), c.ranges, true)
		if err != nil || c.keep != (len(muts) == 1) {
			t.Fatalf("span %s with changed lines %v: kept %d (%v), want kept %v; the spec drops a mutant spanning an unchanged line", c.span, c.ranges, len(muts), err, c.keep)
		}
	}
	skipped := strings.Replace(first, `"worker_outcome": "normal"`, `"worker_outcome": "skipped"`, 1)
	if muts, err := mutantsFromDump("app/mod.py", recordedSource, []byte(skipped), []lineRange{{2, 2}}, true); err != nil || len(muts) != 0 {
		t.Fatalf("skipped job gave %+v, %v; cr-filter-operators marks excluded operators skipped, and they are not mutants", muts, err)
	}
	noDiff := strings.Replace(first, `"diff": "--- mutation diff`, `"nodiff": "`, 1)
	if _, err := mutantsFromDump("app/mod.py", recordedSource, []byte(noDiff), []lineRange{{2, 2}}, true); err == nil {
		t.Fatal("a job without a diff was accepted; without its diff two twins on one span cannot be told apart")
	}
}

// TestChangedFileWithoutMutantsValidates: a changed file whose changed
// lines yield no mutant (an added blank line, a comment) is still a report
// file, and the Stryker schema requires its mutants to be a list; null
// failed validation and with it the whole objective.
func TestChangedFileWithoutMutantsValidates(t *testing.T) {
	for name, dump := range map[string]string{"empty dump": "", "out of range": recordedNumberReplacer} {
		muts, err := mutantsFromDump("app/mod.py", recordedSource, []byte(dump), []lineRange{{1, 1}}, true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rep := newStrykerReport()
		rep.Files["app/mod.py"] = &strykerFile{Language: "python", Source: recordedSource, Mutants: muts}
		data, err := rep.encode()
		if err != nil {
			t.Fatalf("%s: report with a mutant-free changed file fails validation: %v", name, err)
		}
		if !strings.Contains(string(data), `"mutants":[]`) {
			t.Fatalf("%s: report %s; the file is present with an empty mutants list", name, data)
		}
	}
}

// TestApplyDiffRejectsMalformedDiffs: a diff that does not fit the file
// (hunks that overlap, more lines than the file has) must be an error, not
// a crash or a silently wrong replacement and id.
func TestApplyDiffRejectsMalformedDiffs(t *testing.T) {
	src := "x\nx\nx\nx"
	for name, diff := range map[string]string{
		"overlapping hunks":  "--- mutation diff ---\n@@ -1,2 +1,2 @@\n x\n x\n@@ -2,1 +2,1 @@\n x",
		"longer than source": "--- mutation diff ---\n@@ -3,3 +3,3 @@\n x\n x\n x",
	} {
		if got, err := applyDiff(src, diff); err == nil {
			t.Fatalf("%s: applyDiff = %q, nil; want an error", name, got)
		}
	}
}

func TestReplacementTextPositionPastEnd(t *testing.T) {
	for _, p := range [][2]int{{1, 3}, {2, 0}, {3, 0}} {
		if _, err := replacementText("ab", "ab", [2]int{1, 0}, p); err == nil {
			t.Fatalf("end %v in \"ab\" accepted; a position past the end of the file is an error, not a crash", p)
		}
	}
	if r, err := replacementText("abcdef", "a", [2]int{1, 1}, [2]int{1, 2}); err == nil {
		t.Fatalf("replacementText accepted a mutated file shorter than the text around the span (got %q); it must be an error, not a crash", r)
	}
}
