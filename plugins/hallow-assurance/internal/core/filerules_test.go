package core

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const ruleDate = "2026-01-02"

var (
	ruleLevels = []Level{"A", "B", "C", "D"}
	rulePaths  = []string{"pkg/f0.go", "pkg/f1.go", "lib/f2.go", "lib/f3.go", "lib/f4.go"}
	ruleScopes = []string{"**", "pkg/**", "lib/**", "pkg/f0.go", "lib/f3.go"}
)

func ruleObjective(t *rapid.T, id, appliesTo string) Objective {
	o := Objective{ID: id, AppliesTo: appliesTo, Levels: map[Level]string{}}
	for _, l := range ruleLevels {
		if s := rapid.SampledFrom([]string{"", "required", "advisory"}).Draw(t, "status"+string(l)); s != "" {
			o.Levels[l] = s
		}
	}
	return o
}

func ruleWaivers(t *rapid.T, id string) []Waiver {
	var src strings.Builder
	for range rapid.IntRange(0, 2).Draw(t, "nwaivers") {
		src.WriteString(waiverYAML(id, rapid.SampledFrom(ruleScopes).Draw(t, "scope"), "2026-12-31"))
	}
	if src.Len() == 0 {
		return nil
	}
	return mustWaivers(t, src.String())
}

func decideWith(o Objective, changed []ChangedFile, ev Evidence, ws []Waiver) Outcome {
	return DecideObjective(o, changed, ev, ws, nil, ruleDate)
}

func waiverMatches(ws []Waiver, path string) bool {
	return slices.ContainsFunc(ws, func(w Waiver) bool { return globMatch(w.Scope, path) })
}

var fuzzObjective = Objective{ID: "VER-ROBUST-FUZZ", AppliesTo: "inputs", Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"}}

func hookioFile() ChangedFile {
	return ChangedFile{Path: "internal/hookio/hookio.go", Level: "B", Inputs: true, Role: Source}
}

func fuzzSuite(path string, outcomes ...string) FuzzSuite {
	s := FuzzSuite{Sources: []string{path}}
	for i, o := range outcomes {
		s.Cases = append(s.Cases, FuzzCase{Name: fmt.Sprintf("FuzzT%d", i), Outcome: o})
	}
	return s
}

func TestFuzzScenarios(t *testing.T) {
	f := hookioFile()
	decide := func(changed []ChangedFile, run FuzzRun, ws []Waiver) Outcome {
		return decideWith(fuzzObjective, changed, Evidence{Fuzz: &run}, ws)
	}
	t.Run("a file whose package has no fuzz target fails, naming the file", func(t *testing.T) {
		got := decide([]ChangedFile{f}, FuzzRun{Suites: []FuzzSuite{fuzzSuite(f.Path)}}, nil)
		if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), f.Path) {
			t.Fatalf("got %+v; a package without targets gives its inputs no robustness evidence", got)
		}
	})
	t.Run("a file named by no suite fails closed", func(t *testing.T) {
		got := decide([]ChangedFile{f}, FuzzRun{Suites: []FuzzSuite{fuzzSuite("other/x.go", "passed")}}, nil)
		if got.Status != Fail {
			t.Fatalf("got %+v; absent evidence must not read as passing", got)
		}
	})
	t.Run("a skipped case is not a passing seed run", func(t *testing.T) {
		got := decide([]ChangedFile{f}, FuzzRun{Suites: []FuzzSuite{fuzzSuite(f.Path, "passed", "skipped")}}, nil)
		if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "FuzzT1") {
			t.Fatalf("got %+v; a skipped target ran nothing and must name the case", got)
		}
	})
	t.Run("a waiver matching the only failing file waives the objective", func(t *testing.T) {
		ws := mustWaivers(t, waiverYAML("VER-ROBUST-FUZZ", "internal/hookio/**", "2026-12-31"))
		got := decide([]ChangedFile{f}, FuzzRun{Suites: []FuzzSuite{fuzzSuite(f.Path)}}, ws)
		if got.Status != Waived {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("all seeds passing passes", func(t *testing.T) {
		got := decide([]ChangedFile{f}, FuzzRun{Suites: []FuzzSuite{fuzzSuite(f.Path, "passed", "passed")}}, nil)
		if got.Status != Pass {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("only files in an inputs component are decided", func(t *testing.T) {
		other := ChangedFile{Path: "internal/other/o.go", Level: "B", Role: Source}
		got := decide([]ChangedFile{f, other}, FuzzRun{Suites: []FuzzSuite{fuzzSuite(f.Path, "passed")}}, nil)
		if got.Status != Pass {
			t.Fatalf("got %+v; a file outside any inputs component has no fuzz obligation", got)
		}
	})
}

type fuzzFailure struct{ path, c string }

func fuzzWorldGen(t *rapid.T) (Objective, []ChangedFile, FuzzRun, []Waiver) {
	o := ruleObjective(t, "VER-ROBUST-FUZZ", "inputs")
	var changed []ChangedFile
	for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(rulePaths), 1, 5, func(s string) string { return s }).Draw(t, "changed") {
		changed = append(changed, ChangedFile{
			Path:   p,
			Level:  rapid.SampledFrom(ruleLevels).Draw(t, "level "+p),
			Role:   rapid.SampledFrom([]Role{Source, Source, Test, Generated, Config}).Draw(t, "role "+p),
			Inputs: rapid.Bool().Draw(t, "inputs "+p),
		})
	}
	run := FuzzRun{Suites: make([]FuzzSuite, rapid.IntRange(0, 3).Draw(t, "nsuites"))}
	for _, p := range rulePaths {
		if i := rapid.IntRange(-1, len(run.Suites)-1).Draw(t, "suite of "+p); i >= 0 {
			run.Suites[i].Sources = append(run.Suites[i].Sources, p)
		}
	}
	for i := range run.Suites {
		for j := range rapid.IntRange(0, 3).Draw(t, "ncases") {
			run.Suites[i].Cases = append(run.Suites[i].Cases, FuzzCase{
				Name:    fmt.Sprintf("Fuzz%d_%d", i, j),
				Outcome: rapid.SampledFrom([]string{"passed", "passed", "failed", "errored", "skipped"}).Draw(t, "outcome"),
			})
		}
	}
	return o, changed, run, ruleWaivers(t, o.ID)
}

func fuzzOracle(o Objective, changed []ChangedFile, run FuzzRun, ws []Waiver) (Status, []fuzzFailure) {
	var fails []fuzzFailure
	blocking, advisory, waived := false, false, false
	for _, f := range changed {
		status := o.Levels[f.Level]
		if f.Role != Source || !f.Inputs || status == "" {
			continue
		}
		var mine []fuzzFailure
		suite := -1
		for i, s := range run.Suites {
			if slices.Contains(s.Sources, f.Path) {
				suite = i
				break
			}
		}
		switch {
		case suite < 0 || len(run.Suites[suite].Cases) == 0:
			mine = append(mine, fuzzFailure{f.Path, ""})
		default:
			for _, c := range run.Suites[suite].Cases {
				if c.Outcome != "passed" {
					mine = append(mine, fuzzFailure{f.Path, c.Name})
				}
			}
		}
		fails = append(fails, mine...)
		if len(mine) == 0 {
			continue
		}
		if waiverMatches(ws, f.Path) {
			waived = true
			continue
		}
		advisory = true
		blocking = blocking || status == "required"
	}
	return oracleStatus(blocking, advisory, waived), fails
}

func TestFuzzDecisionMatchesTheSpecRecomputation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o, changed, run, ws := fuzzWorldGen(t)
		want, fails := fuzzOracle(o, changed, run, ws)
		got := decideWith(o, changed, Evidence{Fuzz: &run}, ws)
		if got.Status != want {
			t.Fatalf("status %s, spec says %s (changed %+v, run %+v, waivers %+v)", got.Status, want, changed, run, ws)
		}
		for _, f := range fails {
			if !slices.ContainsFunc(got.Details, func(d string) bool { return strings.Contains(d, f.path) && strings.Contains(d, f.c) }) {
				t.Fatalf("no detail names %+v in %q; every failure must name its file and case", f, got.Details)
			}
		}
		if len(got.Details) != len(fails) {
			t.Fatalf("%d details for %d failures: %q", len(got.Details), len(fails), got.Details)
		}
		r := slices.Clone(changed)
		slices.Reverse(r)
		rr := FuzzRun{Suites: slices.Clone(run.Suites)}
		slices.Reverse(rr.Suites)
		for i := range rr.Suites {
			rr.Suites[i].Cases = slices.Clone(rr.Suites[i].Cases)
			slices.Reverse(rr.Suites[i].Cases)
		}
		rw := slices.Clone(ws)
		slices.Reverse(rw)
		again := decideWith(o, r, Evidence{Fuzz: &rr}, rw)
		a, b := slices.Clone(got.Details), slices.Clone(again.Details)
		slices.Sort(a)
		slices.Sort(b)
		if again.Status != got.Status || !slices.Equal(a, b) {
			t.Fatalf("input order changed the decision: %+v vs %+v", got, again)
		}
	})
}

var scenarioBudget = Objective{
	ID: "VER-TEST-BUDGET", Evidence: "test.budget",
	Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"},
	Budget: map[Level]Budget{"A": {Floor: 3, LinesPerCase: 10}, "B": {Floor: 3, LinesPerCase: 15}, "C": {Floor: 3, LinesPerCase: 20}},
}

func scenarioTB(level Level, lines, cases int) TestBudget {
	return TestBudget{Files: []BudgetEntry{
		{Path: "src.go", Level: level, Lines: lines},
		{Path: "src_test.go", Level: level, Test: true, Cases: cases},
	}}
}

func TestBudgetScenarios(t *testing.T) {
	changed := []ChangedFile{{Path: "a/x.go", Level: "B", Role: Source}, {Path: "b/z.go", Level: "B", Role: Source}}
	decide := func(tb TestBudget, ws []Waiver) Outcome {
		return decideWith(scenarioBudget, changed, Evidence{Budget: &tb}, ws)
	}
	t.Run("the floor lets a tiny change carry its tests", func(t *testing.T) {
		if got := decide(scenarioTB("B", 10, 3), nil); got.Status != Pass {
			t.Fatalf("got %+v; 10 lines would allow 1 case but the floor is 3", got)
		}
	})
	t.Run("the allowed count is inclusive and rounds up", func(t *testing.T) {
		if got := decide(scenarioTB("B", 316, 22), nil); got.Status != Pass {
			t.Fatalf("got %+v; ceil(316/15) = 22 cases are allowed", got)
		}
	})
	t.Run("over budget fails with cases, allowance and lines in the detail", func(t *testing.T) {
		got := decide(scenarioTB("B", 316, 23), nil)
		text := strings.Join(got.Details, "\n")
		if got.Status != Fail || !strings.Contains(text, "23 new test cases against 22 allowed for 316 changed source lines") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a waiver covering only part of the level does not excuse it", func(t *testing.T) {
		ws := mustWaivers(t, waiverYAML("VER-TEST-BUDGET", "a/**", "2026-12-31"))
		if got := decide(scenarioTB("B", 316, 23), ws); got.Status != Fail {
			t.Fatalf("got %+v; the failure belongs to every changed file at the level", got)
		}
	})
	t.Run("a waiver covering every changed file at the level waives it", func(t *testing.T) {
		ws := mustWaivers(t, waiverYAML("VER-TEST-BUDGET", "**", "2026-12-31"))
		if got := decide(scenarioTB("B", 316, 23), ws); got.Status != Waived {
			t.Fatalf("got %+v", got)
		}
	})
}

type budgetWorld struct {
	o       Objective
	changed []ChangedFile
	tb      TestBudget
	ws      []Waiver
}

func budgetWorldGen(t *rapid.T) budgetWorld {
	w := budgetWorld{o: ruleObjective(t, "VER-TEST-BUDGET", "")}
	w.o.Evidence = "test.budget"
	w.o.Budget = map[Level]Budget{}
	for _, l := range ruleLevels {
		w.o.Budget[l] = Budget{Floor: rapid.IntRange(1, 4).Draw(t, "floor"), LinesPerCase: rapid.IntRange(1, 20).Draw(t, "lpc")}
	}
	for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(rulePaths), 1, 4, func(s string) string { return s }).Draw(t, "changed") {
		w.changed = append(w.changed, ChangedFile{Path: p, Level: rapid.SampledFrom(ruleLevels).Draw(t, "level "+p), Role: Source})
	}
	w.tb.Files = append(w.tb.Files, BudgetEntry{Path: "t_test.go", Level: rapid.SampledFrom(ruleLevels).Draw(t, "tlevel"), Test: true, Cases: rapid.IntRange(0, 12).Draw(t, "cases")})
	for range rapid.IntRange(0, 5).Draw(t, "nentries") {
		e := BudgetEntry{Path: "e.go", Level: rapid.SampledFrom(ruleLevels).Draw(t, "elevel")}
		if e.Test = rapid.Bool().Draw(t, "istest"); e.Test {
			e.Cases = rapid.IntRange(0, 12).Draw(t, "ecases")
		} else {
			e.Lines = rapid.IntRange(0, 150).Draw(t, "elines")
		}
		w.tb.Files = append(w.tb.Files, e)
	}
	first := &w.tb.Files[0]
	var lines, others int
	for _, e := range w.tb.Files[1:] {
		switch {
		case e.Level != first.Level:
		case e.Test:
			others += e.Cases
		default:
			lines += e.Lines
		}
	}
	if rapid.Bool().Draw(t, "at the boundary") {
		first.Cases = max(0, max(w.o.Budget[first.Level].Floor, ceilDiv(lines, w.o.Budget[first.Level].LinesPerCase))-others+rapid.IntRange(-1, 1).Draw(t, "off by"))
	}
	w.ws = ruleWaivers(t, w.o.ID)
	return w
}

func (w budgetWorld) decide() Outcome {
	return decideWith(w.o, w.changed, Evidence{Budget: &w.tb}, w.ws)
}

func ceilDiv(a, b int) int {
	q := a / b
	if q*b < a {
		q++
	}
	return q
}

func (w budgetWorld) oracle() Status {
	blocking, advisory, waived := false, false, false
	for _, l := range ruleLevels {
		status := w.o.Levels[l]
		var atLevel []string
		for _, f := range w.changed {
			if f.Level == l {
				atLevel = append(atLevel, f.Path)
			}
		}
		if status == "" || len(atLevel) == 0 {
			continue
		}
		var cases, lines int
		for _, e := range w.tb.Files {
			if e.Level == l && e.Test {
				cases += e.Cases
			} else if e.Level == l {
				lines += e.Lines
			}
		}
		b := w.o.Budget[l]
		if cases <= max(b.Floor, ceilDiv(lines, b.LinesPerCase)) {
			continue
		}
		covered := slices.ContainsFunc(w.ws, func(x Waiver) bool {
			return !slices.ContainsFunc(atLevel, func(p string) bool { return !globMatch(x.Scope, p) })
		})
		if covered {
			waived = true
			continue
		}
		advisory = true
		blocking = blocking || status == "required"
	}
	return oracleStatus(blocking, advisory, waived)
}

func TestBudgetOutcomeIsCasesAgainstFloorOrCeilingPerLevel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := budgetWorldGen(t)
		if got, want := w.decide().Status, w.oracle(); got != want {
			t.Fatalf("status %s, spec says %s for %+v", got, want, w)
		}
	})
}

func TestBudgetNeverGetsWorseWhenCasesDecrease(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := budgetWorldGen(t)
		before := w.decide().Status
		i := 0
		w.tb.Files = slices.Clone(w.tb.Files)
		var tests []int
		for j, e := range w.tb.Files {
			if e.Test {
				tests = append(tests, j)
			}
		}
		i = tests[rapid.IntRange(0, len(tests)-1).Draw(t, "which")]
		w.tb.Files[i].Cases = rapid.IntRange(0, w.tb.Files[i].Cases).Draw(t, "fewer")
		if after := w.decide().Status; rank(after) > rank(before) {
			t.Fatalf("removing cases moved %s to %s; fewer tests can never exceed a budget more", before, after)
		}
	})
}

func writeResolutions(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".assure"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ResolutionsFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const resolutionEntry = "- path: internal/core/fast.go\n  function: DecideFast\n  resolution: dead\n  rationale: nothing calls it anywhere\n  approver: owner\n"

func TestResolutionsLoader(t *testing.T) {
	t.Run("a missing file means no resolutions", func(t *testing.T) {
		got, err := LoadResolutions(t.TempDir())
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v; absence is not an error", got, err)
		}
	})
	t.Run("a valid file loads its entries", func(t *testing.T) {
		root := t.TempDir()
		writeResolutions(t, root, resolutionEntry)
		got, err := LoadResolutions(root)
		if err != nil || len(got) != 1 || got[0].Function != "DecideFast" || got[0].Resolution != "dead" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("a duplicate path and function is an error naming the second entry", func(t *testing.T) {
		_, err := ParseResolutions(ResolutionsFile, []byte(resolutionEntry+resolutionEntry))
		if err == nil || !strings.Contains(err.Error(), "/1") || strings.Contains(err.Error(), "/0") {
			t.Fatalf("err = %v; the second entry is the one to delete", err)
		}
	})
	t.Run("the same function in another file is not a duplicate", func(t *testing.T) {
		other := strings.Replace(resolutionEntry, "fast.go", "other.go", 1)
		if got, err := ParseResolutions(ResolutionsFile, []byte(resolutionEntry+other)); err != nil || len(got) != 2 {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}

var coverageObjective = Objective{ID: "VER-COVERAGE-RESOLUTION", Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"}}

const fastPath = "internal/core/fast.go"

func coverageCase(funcs []CoverFunc, zero []int, added []int, res ...string) (ChangedFile, Coverage) {
	cf := CoverFile{Path: fastPath, Funcs: funcs, Lines: map[int]int{}}
	for _, l := range zero {
		cf.Lines[l] = 0
	}
	c := Coverage{Files: []CoverFile{cf}}
	for _, fn := range res {
		c.Resolutions = append(c.Resolutions, Resolution{Path: fastPath, Function: fn, Resolution: "dead"})
	}
	return ChangedFile{Path: fastPath, Level: "B", Role: Source, Lines: added}, c
}

func TestCoverageScenarios(t *testing.T) {
	decl := []CoverFunc{{Name: "DecideFast", Start: 30, End: 50}}
	open := []CoverFunc{{Name: "First", Start: 10}, {Name: "Second", Start: 20}}
	advisoryB := Objective{ID: coverageObjective.ID, Levels: map[Level]string{"B": "advisory"}}
	cases := []struct {
		name    string
		o       Objective
		funcs   []CoverFunc
		zero    []int
		added   []int
		res     []string
		want    Status
		has     []string
		hasNot  []string
		resolve []string
	}{
		{name: "an added zero-hit line in a resolved function is resolved", o: coverageObjective, funcs: decl, zero: []int{40}, added: []int{40}, res: []string{"DecideFast"}, want: Pass, resolve: []string{"DecideFast"}},
		{name: "an unresolved line is named as path:line", o: coverageObjective, funcs: decl, zero: []int{40}, added: []int{40}, want: Fail, has: []string{fastPath + ":40"}},
		{name: "at an advisory level the failure does not block", o: advisoryB, funcs: decl, zero: []int{40}, added: []int{40}, want: AdvisoryFail},
		{name: "zero-hit lines the change did not add are ignored", o: coverageObjective, funcs: decl, zero: []int{40, 41}, added: []int{42}, want: Pass},
		{name: "added lines without a DA record are ignored", o: coverageObjective, funcs: decl, added: []int{40}, want: Pass},
		{name: "a line in no function fails even when every function is resolved", o: coverageObjective, funcs: decl, zero: []int{10}, added: []int{10}, res: []string{"DecideFast"}, want: Fail, has: []string{fastPath + ":10"}},
		{name: "a function without an end line spans to before the next function", o: coverageObjective, funcs: open, zero: []int{19, 20}, added: []int{19, 20}, res: []string{"First"}, want: Fail, has: []string{fastPath + ":20"}, hasNot: []string{fastPath + ":19"}, resolve: []string{"First"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, cov := coverageCase(c.funcs, c.zero, c.added, c.res...)
			got := decideWith(c.o, []ChangedFile{f}, Evidence{Coverage: &cov}, nil)
			text := strings.Join(got.Details, "\n")
			var used []string
			for _, r := range got.Resolutions {
				used = append(used, r.Function)
			}
			if got.Status != c.want || !slices.Equal(used, c.resolve) {
				t.Fatalf("got %+v, want status %s resolving %v", got, c.want, c.resolve)
			}
			for _, s := range c.has {
				if !strings.Contains(text, s) {
					t.Fatalf("details %q lack %q", text, s)
				}
			}
			for _, s := range c.hasNot {
				if strings.Contains(text, s) {
					t.Fatalf("details %q wrongly contain %q", text, s)
				}
			}
		})
	}
	t.Run("a resolution for a removed function is removable and decides nothing", func(t *testing.T) {
		f, c := coverageCase(decl, nil, nil, "oldHelper")
		got := RemovableResolutions(c)
		if len(got) != 1 || got[0].Function != "oldHelper" {
			t.Fatalf("removable = %+v", got)
		}
		if out := decideWith(coverageObjective, []ChangedFile{f}, Evidence{Coverage: &c}, nil); out.Status != Pass {
			t.Fatalf("got %+v; a stale entry must never fail an objective", out)
		}
	})
}

type coverWorld struct {
	o       Objective
	changed []ChangedFile
	cov     Coverage
}

func coverWorldGen(t *rapid.T) coverWorld {
	w := coverWorld{o: ruleObjective(t, "VER-COVERAGE-RESOLUTION", "")}
	var names []string
	for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(rulePaths), 1, 3, func(s string) string { return s }).Draw(t, "paths") {
		cf := CoverFile{Path: p, Lines: map[int]int{}}
		at := rapid.IntRange(1, 6).Draw(t, "first start")
		for i := range rapid.IntRange(0, 4).Draw(t, "nfuncs") {
			fn := CoverFunc{Name: fmt.Sprintf("fn%d", i), Start: at}
			span := rapid.IntRange(1, 8).Draw(t, "span")
			if rapid.Bool().Draw(t, "has end") {
				fn.End = at + span - 1
			}
			cf.Funcs = append(cf.Funcs, fn)
			names = append(names, p+"|"+fn.Name)
			at += span + rapid.IntRange(0, 3).Draw(t, "gap")
		}
		for l := 1; l <= 40; l++ {
			if h := rapid.IntRange(-1, 1).Draw(t, "hits"); h >= 0 {
				cf.Lines[l] = h
			}
		}
		if rapid.IntRange(0, 5).Draw(t, "dropped") > 0 {
			w.cov.Files = append(w.cov.Files, cf)
		}
		cf2 := ChangedFile{
			Path: p, Level: rapid.SampledFrom(ruleLevels).Draw(t, "level"),
			Role:  rapid.SampledFrom([]Role{Source, Source, Source, Test, Generated}).Draw(t, "role"),
			Lines: rapid.SliceOfNDistinct(rapid.IntRange(1, 40), 0, 15, func(i int) int { return i }).Draw(t, "added"),
		}
		w.changed = append(w.changed, cf2)
	}
	names = append(names, "ghost")
	for _, n := range names {
		if rapid.Bool().Draw(t, "resolve "+n) {
			path, fn, _ := strings.Cut(n, "|")
			if fn == "" {
				path, fn = rapid.SampledFrom(rulePaths).Draw(t, "ghost path"), path
			}
			w.cov.Resolutions = append(w.cov.Resolutions, Resolution{Path: path, Function: fn, Resolution: "dead"})
		}
	}
	return w
}

func spanContaining(funcs []CoverFunc, line int) string {
	for _, fn := range funcs {
		end := fn.End
		if end == 0 {
			end = 1 << 30
			for _, o := range funcs {
				if o.Start > fn.Start {
					end = min(end, o.Start-1)
				}
			}
		}
		if fn.Start <= line && line <= end {
			return fn.Name
		}
	}
	return ""
}

type coverExpect struct {
	blocking, advisory bool
	bad                []string
	used               []Resolution
}

func (w coverWorld) resolved(path, fn string) bool {
	return slices.ContainsFunc(w.cov.Resolutions, func(r Resolution) bool { return r.Path == path && r.Function == fn })
}

func (w coverWorld) lineVerdicts(f ChangedFile, cf CoverFile) (bad []string, used []Resolution) {
	for _, l := range f.Lines {
		if h, has := cf.Lines[l]; !has || h > 0 {
			continue
		}
		fn := spanContaining(cf.Funcs, l)
		if fn == "" || !w.resolved(f.Path, fn) {
			bad = append(bad, fmt.Sprintf("%s:%d: ", f.Path, l))
		} else {
			used = append(used, Resolution{Path: f.Path, Function: fn, Resolution: "dead"})
		}
	}
	return bad, used
}

func (w coverWorld) expect() coverExpect {
	records := map[string]CoverFile{}
	for _, f := range w.cov.Files {
		records[f.Path] = f
	}
	var e coverExpect
	for _, f := range w.changed {
		status := w.o.Levels[f.Level]
		if f.Role != Source || status == "" {
			continue
		}
		var bad []string
		cf, ok := records[f.Path]
		if ok {
			var used []Resolution
			bad, used = w.lineVerdicts(f, cf)
			for _, r := range used {
				if !slices.Contains(e.used, r) {
					e.used = append(e.used, r)
				}
			}
		} else {
			bad = []string{f.Path + ": "}
		}
		e.bad = append(e.bad, bad...)
		if len(bad) > 0 {
			e.advisory = true
			e.blocking = e.blocking || status == "required"
		}
	}
	return e
}

func (w coverWorld) removable() []Resolution {
	records := map[string]CoverFile{}
	for _, f := range w.cov.Files {
		records[f.Path] = f
	}
	var out []Resolution
	for _, r := range w.cov.Resolutions {
		if cf, ok := records[r.Path]; ok && !slices.ContainsFunc(cf.Funcs, func(fn CoverFunc) bool { return fn.Name == r.Function }) {
			out = append(out, r)
		}
	}
	return out
}

func sameResolutions(a, b []Resolution) bool {
	byKey := func(x, y Resolution) int {
		return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Function, y.Function))
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, byKey)
	slices.SortFunc(b, byKey)
	return slices.EqualFunc(a, b, func(x, y Resolution) bool { return byKey(x, y) == 0 })
}

func TestCoverageDecisionMatchesTheSpecRecomputation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := coverWorldGen(t)
		want := w.expect()
		got := decideWith(w.o, w.changed, Evidence{Coverage: &w.cov}, nil)
		if wantStatus := oracleStatus(want.blocking, want.advisory, false); got.Status != wantStatus {
			t.Fatalf("status %s, spec says %s for %+v", got.Status, wantStatus, w)
		}
		if len(got.Details) != len(want.bad) {
			t.Fatalf("details %q, want one per %q", got.Details, want.bad)
		}
		for _, d := range want.bad {
			if !slices.ContainsFunc(got.Details, func(g string) bool { return strings.Contains(g, d) }) {
				t.Fatalf("no detail names %q in %q", d, got.Details)
			}
		}
		if !sameResolutions(got.Resolutions, want.used) {
			t.Fatalf("resolutions used %+v, want %+v", got.Resolutions, want.used)
		}
		if !sameResolutions(RemovableResolutions(w.cov), w.removable()) {
			t.Fatalf("removable %+v, want %+v", RemovableResolutions(w.cov), w.removable())
		}
	})
}
