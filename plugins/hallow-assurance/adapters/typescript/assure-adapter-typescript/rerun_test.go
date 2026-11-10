package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

// rerunBatch is what one rerun round of one unit must select: the absolute test files holding
// the still-failing tests, their full names, and names in those files (or elsewhere) that the
// name pattern must not match.
type rerunBatch struct {
	files      []string
	names      []string
	neighbours []string
}

type rerunArgs struct {
	files, patterns []string
	outputFile      string
}

func parseRerunArgs(c call) rerunArgs {
	var r rerunArgs
	for i := 0; i < len(c.args); i++ {
		a := c.args[i]
		switch {
		case a == "-t" || a == "--testNamePattern":
			if i+1 < len(c.args) {
				r.patterns = append(r.patterns, c.args[i+1])
				i++
			}
		case strings.HasPrefix(a, "-t="):
			r.patterns = append(r.patterns, strings.TrimPrefix(a, "-t="))
		case strings.HasPrefix(a, "--testNamePattern="):
			r.patterns = append(r.patterns, strings.TrimPrefix(a, "--testNamePattern="))
		case a == "--outputFile":
			if i+1 < len(c.args) {
				r.outputFile = c.args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--outputFile="):
			r.outputFile = strings.TrimPrefix(a, "--outputFile=")
		case strings.HasPrefix(a, "-"):
		case hasSourceExt(a):
			p := a
			if !filepath.IsAbs(p) {
				p = filepath.Join(c.cwd, p)
			}
			r.files = append(r.files, realPath(p))
		}
	}
	slices.Sort(r.files)
	r.files = slices.Compact(r.files)
	return r
}

// checkRerunCall: a rerun round runs vitest once, not in watch mode, in the unit with the first
// run's ASSURE_SEED and shuffle flag, on exactly the files holding the round's failing tests, with
// one name pattern that matches each failing test's full name exactly and nothing else. vitest
// compiles the pattern with new RegExp; Go's regexp reads an escaped literal the same way.
func checkRerunCall(t fataler, round int, top string, first, rc call, b rerunBatch) {
	if rc.cwd != first.cwd {
		t.Fatalf("rerun %d ran vitest in %s, the first run in %s; the unit's vitest config applies only from the unit directory", round, rc.cwd, first.cwd)
	}
	if rc.seed == "" || rc.seed != first.seed {
		t.Fatalf("rerun %d saw ASSURE_SEED %q, the first run %q; a rerun under another seed draws property inputs the first run never saw, so its pass says nothing about the first failure", round, rc.seed, first.seed)
	}
	if len(rc.args) == 0 || (rc.args[0] != "run" && !slices.Contains(rc.args, "--run")) {
		t.Fatalf("rerun %d args %q; a rerun runs once (vitest run or --run), never in watch mode", round, rc.args)
	}
	if !slices.Contains(rc.args, "--sequence.shuffle") {
		t.Fatalf("rerun %d args %q lack --sequence.shuffle; the protocol reruns with the first run's flags", round, rc.args)
	}
	ra := parseRerunArgs(rc)
	if ra.outputFile == "" || strings.HasPrefix(realPath(ra.outputFile), top+string(filepath.Separator)) {
		t.Fatalf("rerun %d --outputFile %q; without a report path outside the repository vitest 5 writes .vitest/json/output.json into it, and the adapter has no result to read", round, ra.outputFile)
	}
	want := slices.Clone(b.files)
	for i := range want {
		want[i] = realPath(want[i])
	}
	slices.Sort(want)
	if !slices.Equal(ra.files, want) {
		t.Fatalf("rerun %d test files %q, want %q; a round runs only the files holding a still-failing test, and the file is what keeps two same-named tests in different files apart", round, ra.files, want)
	}
	if len(ra.patterns) != 1 {
		t.Fatalf("rerun %d name patterns %q, want exactly one; without it every test in the file reruns beside the failing ones", round, ra.patterns)
	}
	checkRerunPattern(t, round, ra.patterns[0], b)
}

func checkRerunPattern(t fataler, round int, pattern string, b rerunBatch) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("rerun %d pattern %q does not compile: %v", round, pattern, err)
	}
	for _, n := range b.names {
		if !re.MatchString(n) {
			t.Fatalf("rerun %d pattern %q does not match failing test %q; vitest matches it against the full name (describe titles and title joined by \" > \"), and a test name holding regexp metacharacters must be escaped or the rerun selects nothing", round, pattern, n)
		}
		for _, near := range []string{n + " x", "x " + n} {
			if re.MatchString(near) {
				t.Fatalf("rerun %d pattern %q matches %q; it must be anchored to the full name of %q, or tests sharing a prefix or suffix rerun too", round, pattern, near, n)
			}
		}
	}
	for _, n := range b.neighbours {
		if re.MatchString(n) {
			t.Fatalf("rerun %d pattern %q matches %q, which is not failing; only the failing tests rerun (names %q)", round, pattern, n, b.names)
		}
	}
}

var rerunNamePool = []string{
	"parse", "parse > rejects empty", "parse > rejects (empty)+", "parse > rejects empty > deep",
	"a.b*c", "x|y", "^$", "[z] {2}", `back\slash`, "?",
}

var rerunFiles = []string{"src/p.test.ts", "src/q.test.ts", "src/r.test.ts"}

type rrTest struct {
	name, status string
	passes       []bool
}

type rrFile struct {
	rel     string
	loadErr bool
	tests   []rrTest
}

type rrUnit struct {
	dir      string
	noReport bool
	files    []rrFile
	faults   []string
}

type rrKey struct{ file, name string }

type rrPlan struct {
	replay   replayed
	outcomes map[string]ev.Outcome
	flaky    map[string]int
	batches  []rerunBatch
}

func drawUnit(t *rapid.T, dir string) rrUnit {
	u := rrUnit{dir: dir, noReport: rapid.IntRange(0, 9).Draw(t, dir+" first run has no report") == 0}
	for _, f := range rerunFiles {
		if !rapid.Bool().Draw(t, dir+" reports "+f) {
			continue
		}
		rf := rrFile{rel: f, loadErr: rapid.IntRange(0, 5).Draw(t, dir+" "+f+" load error") == 0}
		if !rf.loadErr {
			names := rapid.SliceOfNDistinct(rapid.SampledFrom(rerunNamePool), 1, 5, func(s string) string { return s }).Draw(t, dir+" "+f+" names")
			for _, n := range names {
				rf.tests = append(rf.tests, rrTest{
					name:   n,
					status: rapid.SampledFrom([]string{"passed", "failed", "failed", "skipped", "bogus"}).Draw(t, dir+" "+f+" "+n+" status"),
					passes: rapid.SliceOfN(rapid.Bool(), adapterkit.RerunRounds, adapterkit.RerunRounds).Draw(t, dir+" "+f+" "+n+" passes per rerun"),
				})
			}
		}
		u.files = append(u.files, rf)
	}
	u.faults = rapid.SliceOfN(rapid.SampledFrom([]string{"", "", "", "no report", "empty report"}), adapterkit.RerunRounds, adapterkit.RerunRounds).Draw(t, dir+" rerun faults")
	return u
}

func assertion(name, status, msg string) vitestAssertion {
	parts := strings.Split(name, " > ")
	a := vitestAssertion{AncestorTitles: parts[:len(parts)-1], Title: parts[len(parts)-1], Status: status, FailureMessages: []string{}}
	if status == "failed" {
		a.FailureMessages = []string{msg}
	}
	return a
}

func reportJSON(t fataler, r vitestReport) string {
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(data)
}

func (u rrUnit) abs(top, rel string) string {
	return filepath.Join(top, filepath.FromSlash(u.dir), filepath.FromSlash(rel))
}

func (u rrUnit) classname(rel string) string { return u.dir + "/" + rel }

// plan is the specification's answer for one unit: what the first run and each rerun round
// replay when the adapter selects correctly, the evidence that must result, and what each
// round must select.
func (u rrUnit) plan(t fataler, top string) rrPlan {
	p, tests, failing := u.firstRun(t, top)
	if u.noReport {
		return p
	}
	still := failing
	for round := 1; round <= adapterkit.RerunRounds && len(still) > 0; round++ {
		b, rr := u.rerunRound(t, top, round, still)
		p.batches = append(p.batches, b)
		switch u.faults[round-1] {
		case "no report":
			p.replay.reruns = append(p.replay.reruns, replayed{noReport: true, code: 1})
			continue
		case "empty report":
			p.replay.reruns = append(p.replay.reruns, replayed{report: `{"testResults":[]}`, code: 0})
			continue
		}
		p.replay.reruns = append(p.replay.reruns, rr)
		var next []rrKey
		for _, k := range still {
			if tests[k].passes[round-1] {
				p.flaky[k.file+"."+k.name] = round
			} else {
				next = append(next, k)
			}
		}
		still = next
	}
	return p
}

func (u rrUnit) firstRun(t fataler, top string) (rrPlan, map[rrKey]rrTest, []rrKey) {
	p := rrPlan{outcomes: map[string]ev.Outcome{}, flaky: map[string]int{}}
	abs := func(rel string) string { return u.abs(top, rel) }
	cn := u.classname
	tests := map[rrKey]rrTest{}
	var failing []rrKey
	first := vitestReport{TestResults: []vitestFile{}}
	code := 0
	for _, f := range u.files {
		vf := vitestFile{Name: abs(f.rel), Status: "passed", AssertionResults: []vitestAssertion{}}
		if f.loadErr {
			vf.Status, vf.Message, code = "failed", "Cannot find module './gone' imported from "+abs(f.rel), 1
			p.outcomes[cn(f.rel)+".(file)"] = ev.Errored
		}
		for _, tc := range f.tests {
			k := rrKey{cn(f.rel), tc.name}
			tests[k] = tc
			vf.AssertionResults = append(vf.AssertionResults, assertion(tc.name, tc.status, "first run of "+tc.name))
			switch tc.status {
			case "passed":
				p.outcomes[k.file+"."+k.name] = ev.Passed
			case "skipped":
				p.outcomes[k.file+"."+k.name] = ev.Skipped
			case "failed":
				p.outcomes[k.file+"."+k.name] = ev.Failed
				failing = append(failing, k)
				vf.Status, code = "failed", 1
			default:
				p.outcomes[k.file+"."+k.name] = ev.Errored
				vf.Status, code = "failed", 1
			}
		}
		first.TestResults = append(first.TestResults, vf)
	}
	p.replay = replayed{report: reportJSON(t, first), code: code}
	if u.noReport {
		p.replay = replayed{noReport: true, code: 1, stderr: "Error: failed to load config"}
		p.outcomes = map[string]ev.Outcome{"unit " + u.dir + ".(run)": ev.Errored}
		failing = nil
	}
	return p, tests, failing
}

// rerunRound is what a correct round selects for the still-failing tests, and the report vitest
// gives for it: the selected tests with their outcome in this round, a same-named test in a
// selected file passing beside them (the pattern cannot tell files apart), and every other test
// in a selected file skipped.
func (u rrUnit) rerunRound(t fataler, top string, round int, still []rrKey) (rerunBatch, replayed) {
	inBatch := map[rrKey]bool{}
	files := map[string]bool{}
	names := map[string]bool{}
	for _, k := range still {
		inBatch[k], files[k.file], names[k.name] = true, true, true
	}
	b := rerunBatch{names: slices.Sorted(maps.Keys(names))}
	report := vitestReport{TestResults: []vitestFile{}}
	code := 0
	neighbours := map[string]bool{}
	for _, n := range rerunNamePool {
		neighbours[n] = true
	}
	for _, f := range u.files {
		if !files[u.classname(f.rel)] {
			continue
		}
		b.files = append(b.files, u.abs(top, f.rel))
		vf := vitestFile{Name: u.abs(top, f.rel), Status: "passed", AssertionResults: []vitestAssertion{}}
		for _, tc := range f.tests {
			neighbours[tc.name] = true
			k := rrKey{u.classname(f.rel), tc.name}
			status := "skipped"
			switch {
			case inBatch[k] && tc.passes[round-1]:
				status = "passed"
			case inBatch[k]:
				status, vf.Status, code = "failed", "failed", 1
			case names[tc.name]:
				status = "passed"
			}
			vf.AssertionResults = append(vf.AssertionResults, assertion(tc.name, status, fmt.Sprintf("rerun %d of %s", round, tc.name)))
		}
		report.TestResults = append(report.TestResults, vf)
	}
	for n := range names {
		delete(neighbours, n)
	}
	b.neighbours = slices.Sorted(maps.Keys(neighbours))
	return b, replayed{report: reportJSON(t, report), code: code}
}

func rerunRepo(t *testing.T, units []string) (top, data string) {
	files := map[string]string{".gitignore": "node_modules\n"}
	tools := map[string]map[string]string{}
	for _, unit := range units {
		files[unit+"/package.json"] = "{}"
		files[unit+"/package-lock.json"] = "{}"
		for _, f := range rerunFiles {
			files[unit+"/"+f] = ""
		}
		tools[unit] = map[string]string{"vitest": "5.0.4"}
	}
	isolateGit(t)
	fakeNodeOnPath(t)
	top, data = realTempDir(t), t.TempDir()
	buildRepo(t, top, data, files, tools)
	for _, unit := range units {
		for _, f := range rerunFiles {
			writeTree(t, top, map[string]string{unit + "/" + f: "changed\n"})
		}
	}
	t.Chdir(top)
	return top, data
}

func flakyProps(c adapterkit.XMLCase) []string {
	var vs []string
	for _, p := range c.Properties {
		if p.Name == "assure.flaky" {
			vs = append(vs, p.Value)
		}
	}
	return vs
}

// TestRunTestsRerunsFailingTestsByFileAndName: for any mix of passing, failing, skipped,
// unknown-status and load-error results in two units, each unit reruns its failing tests in at
// most 3 rounds, one vitest call per round, each selecting the round's still-failing tests by
// file and exact full name. A failing test is flaky on the first round whose report shows it
// passing in its own file; a round with no report, or a report without the test, or a same-named
// test passing in another file, is a failing round for it. The evidence keeps the first run's
// cases and failure text, and nothing but a flaky failure carries assure.flaky.
func TestRunTestsRerunsFailingTestsByFileAndName(t *testing.T) {
	t.Setenv("ASSURE_SEED", "0")
	units := []string{"packages/a", "packages/b"}
	top, data := rerunRepo(t, units)
	seed := checkSeed(t, headOf(t, top))
	rapid.Check(t, func(t *rapid.T) {
		plans := map[string]rrPlan{}
		want := map[string]ev.Outcome{}
		flaky := map[string]int{}
		for _, unit := range units {
			p := drawUnit(t, unit).plan(t, top)
			plans[unit] = p
			maps.Copy(want, p.outcomes)
			maps.Copy(flaky, p.flaky)
			replay(t, filepath.Join(data, unit), "vitest", p.replay)
		}
		out, err := os.MkdirTemp(data, "out")
		if err != nil {
			t.Fatalf("%v", err)
		}
		j := junitIn(t, out, runIn(t, "VER-TESTS-PASS", out))
		for _, unit := range units {
			checkUnitReruns(t, top, filepath.Join(data, unit), unit, seed, plans[unit])
		}
		if got := outcomes(j); !maps.Equal(got, want) {
			t.Fatalf("cases %v\nwant %v; reruns change no outcome and add no case: a flaky failure stays a failure, and a rerun result is never a second case", got, want)
		}
		checkFlakyFailures(t, j, flaky)
		checkFlakyProperties(t, out, flaky)
	})
}

func checkUnitReruns(t fataler, top, data, unit, seed string, p rrPlan) {
	calls := callsOf(t, data, "vitest")
	if len(calls) != 1+len(p.batches) {
		t.Fatalf("unit %s: vitest ran %d times, want %d (the first run and one call per rerun round, while a failing test is left; at most %d rounds)", unit, len(calls), 1+len(p.batches), adapterkit.RerunRounds)
	}
	if calls[0].seed != seed {
		t.Fatalf("unit %s: first run ASSURE_SEED %q, want %q from HEAD", unit, calls[0].seed, seed)
	}
	for k, b := range p.batches {
		checkRerunCall(t, k+1, top, calls[0], calls[k+1], b)
	}
}

func checkFlakyFailures(t fataler, j ev.JUnit, flaky map[string]int) {
	for _, f := range j.Failing {
		if f.Error {
			if f.Flaky != 0 {
				t.Fatalf("erroring case %s is flaky %d; a case that did not run is never rerun", f.Name, f.Flaky)
			}
			continue
		}
		if f.Flaky != flaky[f.Name] {
			t.Fatalf("%s flaky %d, want %d; it is flaky on the first round whose own-file result passed, and a round without its result counts as failing", f.Name, f.Flaky, flaky[f.Name])
		}
		if !strings.Contains(f.Text, "first run of") || strings.Contains(f.Text, "rerun ") {
			t.Fatalf("%s text %q; the first run's failure stays in the evidence, so the human sees what went wrong", f.Name, f.Text)
		}
	}
}

func checkFlakyProperties(t fataler, out string, flaky map[string]int) {
	raw, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	var s adapterkit.XMLSuites
	if err := xml.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%v", err)
	}
	for _, su := range s.Suites {
		for _, c := range su.Cases {
			var want []string
			if k := flaky[c.Classname+"."+c.Name]; k > 0 {
				want = []string{strconv.Itoa(k)}
			}
			if got := flakyProps(c); !slices.Equal(got, want) {
				t.Fatalf("case %s %s assure.flaky %q, want %q\n%s", c.Classname, c.Name, got, want, raw)
			}
		}
	}
}
