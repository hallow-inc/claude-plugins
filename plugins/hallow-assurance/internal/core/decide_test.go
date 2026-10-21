package core

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type fataler interface {
	Helper()
	Fatal(args ...any)
}

func mustWaivers(t fataler, yaml string) []Waiver {
	t.Helper()
	ws, err := ParseWaivers("waivers.yaml", []byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func waiverYAML(objective, scope, expires string) string {
	return "- objective: " + objective + "\n  scope: \"" + scope + "\"\n  rationale: generated rationale long enough\n  approver: owner\n  expires: " + expires + "\n"
}

var fuzzB = Objective{ID: "VER-ROBUST-FUZZ", Evidence: "fuzz.run", AppliesTo: "inputs", Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"}}

func crashingFuzz(path string) Evidence {
	return Evidence{Fuzz: &FuzzRun{Suites: []FuzzSuite{{Sources: []string{path}, Cases: []FuzzCase{{Name: "FuzzDecode", Outcome: "failed"}}}}}}
}

func inputsFile(path string, l Level) ChangedFile {
	return ChangedFile{Path: path, Level: l, Inputs: true, Role: Source}
}

func TestScopedWaiverSuppressesALocatedFinding(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("VER-ROBUST-FUZZ", "internal/decode/**", "2026-12-31"))
	changed := []ChangedFile{inputsFile("internal/decode/decode.go", "B")}
	ev := crashingFuzz("internal/decode/decode.go")
	if got := DecideObjective(fuzzB, changed, ev, nil, "2026-09-27"); got.Status != Fail {
		t.Fatalf("without the waiver got %+v; the crashing fuzz target must fail, or the waiver below proves nothing", got)
	}
	got := DecideObjective(fuzzB, changed, ev, ws, "2026-09-27")
	if got.Status != Waived || len(got.Waivers) != 1 {
		t.Fatalf("got %+v; a located failure inside an active waiver's scope must not count", got)
	}
}

func TestPartialScopeDoesNotCoverMissingEvidence(t *testing.T) {
	o := Objective{ID: "IND-VERIFIER-DISTINCT", Levels: map[Level]string{"A": "required", "B": "required"}}
	ws := mustWaivers(t, waiverYAML("IND-VERIFIER-DISTINCT", "internal/**", "2026-12-31"))
	changed := []ChangedFile{{Path: "internal/core/fast.go", Level: "B"}, {Path: "cmd/assure/main.go", Level: "B"}}
	got := DecideObjective(o, changed, Evidence{Problems: []string{"no adapter lists IND-VERIFIER-DISTINCT"}}, ws, "2026-09-27")
	if got.Status != Fail {
		t.Fatalf("got %+v; a narrow waiver must not hide a failure that also covers cmd/", got)
	}
}

func TestRepoWideWaiverCoversMissingEvidence(t *testing.T) {
	o := Objective{ID: "IND-VERIFIER-DISTINCT", Levels: map[Level]string{"A": "required", "B": "required"}}
	ws := mustWaivers(t, waiverYAML("IND-VERIFIER-DISTINCT", "**", "2026-12-31"))
	changed := []ChangedFile{{Path: "internal/core/fast.go", Level: "B"}, {Path: "cmd/assure/main.go", Level: "B"}}
	got := DecideObjective(o, changed, Evidence{Problems: []string{"no adapter lists IND-VERIFIER-DISTINCT"}}, ws, "2026-09-27")
	if got.Status != Waived {
		t.Fatalf("got %+v", got)
	}
}

func TestWaiverIsActiveOnItsLastDayOnly(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("VER-ROBUST-FUZZ", "**", "2026-01-02"))
	ev := crashingFuzz("a.go")
	changed := []ChangedFile{inputsFile("a.go", "B")}
	if got := DecideObjective(fuzzB, changed, ev, ws, "2026-01-02"); got.Status != Waived {
		t.Fatalf("on the expiry date: got %s, want waived", got.Status)
	}
	if got := DecideObjective(fuzzB, changed, ev, ws, "2026-01-03"); got.Status != Fail || len(got.Waivers) != 0 {
		t.Fatalf("after expiry: got %+v; an expired waiver must suppress nothing", got)
	}
	if exp := Expired(ws, "2026-01-03"); len(exp) != 1 {
		t.Fatalf("Expired = %v", exp)
	}
}

func TestWaiverForAnotherObjectiveDoesNothing(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("VER-MUTATION-CHANGED", "**", "2026-12-31"))
	if got := DecideObjective(fuzzB, []ChangedFile{inputsFile("a.go", "B")}, crashingFuzz("a.go"), ws, "2026-09-27"); got.Status != Fail || len(got.Waivers) != 0 {
		t.Fatalf("got %+v; a waiver for VER-MUTATION-CHANGED must not hide a VER-ROBUST-FUZZ crash", got)
	}
}

func TestLevelDSkipsLevelBOnlyObjectives(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(c.Objectives, func(o Objective) bool { return o.ID == "VER-MUTATION-CHANGED" })
	if i < 0 {
		t.Fatal("VER-MUTATION-CHANGED missing from catalog v0")
	}
	o := c.Objectives[i]
	if _, listed := o.Levels["D"]; listed {
		t.Fatalf("VER-MUTATION-CHANGED lists level D (%v); the scenario needs an objective with no level D entry", o.Levels)
	}
	if Applicable(o, []ChangedFile{{Path: "a.go", Level: "D"}}) {
		t.Fatal("VER-MUTATION-CHANGED has no level D entry but was applicable to a level-D-only change")
	}
	if !Applicable(o, []ChangedFile{{Path: "a.go", Level: "D"}, {Path: "b.go", Level: "B"}}) {
		t.Fatal("VER-MUTATION-CHANGED not applicable once a level-B file changed too")
	}
}

func TestFormalObjectivesNeedAFormalComponent(t *testing.T) {
	o := Objective{ID: "FM-COMPLETE", AppliesTo: "formal", Levels: map[Level]string{"A": "advisory", "B": "advisory", "C": "advisory"}}
	if Applicable(o, []ChangedFile{{Path: "a.go", Level: "B"}}) {
		t.Fatal("FM-COMPLETE applicable without a formal component")
	}
	if !Applicable(o, []ChangedFile{{Path: "a.go", Level: "B", Formal: true}}) {
		t.Fatal("FM-COMPLETE not applicable in a formal component")
	}
}

func TestObjectiveNoAdapterProducesFails(t *testing.T) {
	o := Objective{ID: "IND-VERIFIER-DISTINCT", Levels: map[Level]string{"A": "required", "B": "required"}}
	got := DecideObjective(o, []ChangedFile{{Path: "a.go", Level: "B"}}, Evidence{Problems: []string{"no adapter lists IND-VERIFIER-DISTINCT"}}, nil, "2026-09-27")
	if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "no adapter lists") {
		t.Fatalf("got %+v; missing evidence must fail closed", got)
	}
}

type world struct {
	o       Objective
	changed []ChangedFile
	levelOf map[string]Level
	fuzz    map[string]string
	ev      Evidence
	ws      []Waiver
	date    string
}

var (
	paths        = []string{"a/x.go", "a/y.go", "b/z.go"}
	scopes       = []string{"**", "a/**", "b/**", "a/x.go"}
	dates        = []string{"2026-01-01", "2026-01-02", "2026-01-03"}
	fuzzOutcomes = []string{"unnamed", "no-target", fuzzPassed, "failed"}
)

func worldGen(t *rapid.T) world {
	w := world{o: objectiveGen(t), levelOf: map[string]Level{}, fuzz: map[string]string{}, date: "2026-01-02"}
	for _, p := range paths {
		w.levelOf[p] = levelGen.Draw(t, "level "+p)
	}
	for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(paths), 1, 3, func(s string) string { return s }).Draw(t, "changed") {
		role := rapid.SampledFrom([]Role{Source, Test}).Draw(t, "role "+p)
		w.changed = append(w.changed, ChangedFile{Path: p, Level: w.levelOf[p], Role: role})
	}
	for range rapid.IntRange(0, 1).Draw(t, "nproblems") {
		w.ev.Problems = append(w.ev.Problems, "no evidence")
	}
	for range rapid.IntRange(0, 1).Draw(t, "nfailing") {
		w.ev.Failing = append(w.ev.Failing, TestFailure{Name: "pkg.TestX", Text: "failing test: pkg.TestX"})
	}
	if rapid.Bool().Draw(t, "hasfuzz") {
		run := FuzzRun{}
		for _, p := range paths {
			outcome := rapid.SampledFrom(fuzzOutcomes).Draw(t, "fuzz "+p)
			w.fuzz[p] = outcome
			switch outcome {
			case "no-target":
				run.Suites = append(run.Suites, FuzzSuite{Sources: []string{p}})
			case fuzzPassed, "failed":
				run.Suites = append(run.Suites, FuzzSuite{Sources: []string{p}, Cases: []FuzzCase{{Name: "Fuzz" + p, Outcome: outcome}}})
			}
		}
		w.ev.Fuzz = &run
	}
	var src strings.Builder
	for range rapid.IntRange(0, 2).Draw(t, "nwaivers") {
		obj := rapid.SampledFrom([]string{"VER-X", "VER-Y"}).Draw(t, "wobj")
		src.WriteString(waiverYAML(obj, rapid.SampledFrom(scopes).Draw(t, "scope"), rapid.SampledFrom(dates).Draw(t, "expires")))
	}
	if src.Len() > 0 {
		w.ws = mustWaivers(t, src.String())
	}
	return w
}

func (w world) decide() Outcome {
	return DecideObjective(w.o, w.changed, w.ev, w.ws, w.date)
}

func globMatch(scope, p string) bool {
	g, _ := CompileGlob(scope)
	return g.Match(p)
}

func (w world) oracleActive() []Waiver {
	var active []Waiver
	for _, x := range w.ws {
		if x.Objective == w.o.ID && x.Expires >= w.date {
			active = append(active, x)
		}
	}
	return active
}

func (w world) oracleApplicable() (req, adv bool, paths []string) {
	for _, c := range w.changed {
		s := w.o.Levels[c.Level]
		req, adv = req || s == "required", adv || s == "advisory"
		if s != "" {
			paths = append(paths, c.Path)
		}
	}
	return req, adv, paths
}

func oracleCovered(active []Waiver, paths []string) bool {
	return len(paths) > 0 && slices.ContainsFunc(active, func(x Waiver) bool {
		return !slices.ContainsFunc(paths, func(p string) bool { return !globMatch(x.Scope, p) })
	})
}

func (w world) oracleLocated() []ChangedFile {
	if w.ev.Fuzz == nil {
		return nil
	}
	var out []ChangedFile
	for _, c := range w.changed {
		if c.Role == Source && w.fuzz[c.Path] != fuzzPassed {
			out = append(out, c)
		}
	}
	return out
}

func evalOracle(w world) Status {
	active := w.oracleActive()
	req, adv, applicable := w.oracleApplicable()
	unlocated := len(w.ev.Problems) + len(w.ev.Failing)
	blocking, advisory, waived := false, false, false
	if unlocated > 0 && (req || adv) {
		waived = oracleCovered(active, applicable)
		blocking, advisory = req && !waived, !waived
	}
	for _, c := range w.oracleLocated() {
		status := w.o.Levels[c.Level]
		if status == "" {
			continue
		}
		if slices.ContainsFunc(active, func(x Waiver) bool { return globMatch(x.Scope, c.Path) }) {
			waived = true
			continue
		}
		advisory = true
		blocking = blocking || (status == "required" && req)
	}
	return oracleStatus(blocking, advisory, waived)
}

func oracleStatus(blocking, advisory, waived bool) Status {
	switch {
	case blocking:
		return Fail
	case advisory:
		return AdvisoryFail
	case waived:
		return Waived
	}
	return Pass
}

func TestDecisionMatchesTheEvaluationRules(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := worldGen(t)
		if got, want := w.decide().Status, evalOracle(w); got != want {
			t.Fatalf("DecideObjective = %s, rules say %s for %+v", got, want, w)
		}
	})
}

func TestMissingEvidenceIsWaivedExactlyWhenCovered(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := worldGen(t)
		w.ev = Evidence{Problems: []string{"no adapter lists VER-X"}}
		req, adv, applicable := changedStatus(w.o, w.changed)
		if !req && !adv {
			t.Skip("not applicable")
		}
		covered := slices.ContainsFunc(w.ws, func(x Waiver) bool {
			return x.Objective == "VER-X" && x.Expires >= w.date && !slices.ContainsFunc(applicable, func(p string) bool { return !globMatch(x.Scope, p) })
		})
		if got := w.decide().Status; (got == Waived) != covered {
			t.Fatalf("status %s, covered %v, applicable %v, waivers %+v", got, covered, applicable, w.ws)
		}
	})
}

func TestWaivedOnlyWhenEveryFailureWasWaived(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := worldGen(t)
		got := w.decide()
		withoutWaivers := DecideObjective(w.o, w.changed, w.ev, nil, w.date)
		switch {
		case got.Status == Waived && (withoutWaivers.Status == Pass || len(got.Waivers) == 0):
			t.Fatalf("waived with nothing suppressed by a waiver: %+v vs %+v", got, withoutWaivers)
		case got.Status == Pass && withoutWaivers.Status != Pass:
			t.Fatalf("waivers turned %s into pass instead of waived", withoutWaivers.Status)
		case rank(got.Status) > rank(withoutWaivers.Status):
			t.Fatalf("adding waivers made the status worse: %s -> %s", withoutWaivers.Status, got.Status)
		}
	})
}

func TestDecisionIgnoresInputOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := worldGen(t)
		a := w.decide()
		r := w
		r.changed = slices.Clone(w.changed)
		slices.Reverse(r.changed)
		r.ev.Problems = slices.Clone(w.ev.Problems)
		slices.Reverse(r.ev.Problems)
		if w.ev.Fuzz != nil {
			suites := slices.Clone(w.ev.Fuzz.Suites)
			slices.Reverse(suites)
			r.ev.Fuzz = &FuzzRun{Suites: suites}
		}
		r.ws = slices.Clone(w.ws)
		slices.Reverse(r.ws)
		b := r.decide()
		if a.Status != b.Status || !slices.Equal(a.Details, b.Details) ||
			!slices.EqualFunc(a.Waivers, b.Waivers, func(x, y Waiver) bool { return compareWaivers(x, y) == 0 }) {
			t.Fatalf("order changed the outcome: %+v vs %+v", a, b)
		}
	})
}

func TestFixObjectivesNeedAFixCommit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		levelsGen := rapid.SampledFrom([]Level{"A", "B", "C", "D"})
		o := Objective{ID: "VER-FAIL-ON-BASE", AppliesTo: "fix", Levels: map[Level]string{}}
		for _, l := range []Level{"A", "B", "C", "D"} {
			if s := rapid.SampledFrom([]string{"", "required", "advisory"}).Draw(t, "status"+string(l)); s != "" {
				o.Levels[l] = s
			}
		}
		fix := rapid.Bool().Draw(t, "fix")
		var changed []ChangedFile
		listed := false
		for i := range rapid.IntRange(0, 4).Draw(t, "n") {
			f := ChangedFile{Path: fmt.Sprintf("f%d.go", i), Level: levelsGen.Draw(t, "level"), Fix: fix, Formal: rapid.Bool().Draw(t, "formal")}
			listed = listed || o.Levels[f.Level] != ""
			changed = append(changed, f)
		}
		if got, want := Applicable(o, changed), fix && listed; got != want {
			t.Fatalf("Applicable = %v, want %v (fix %v, listed %v); a fix objective must apply exactly to fix changes at a listed level", got, want, fix, listed)
		}
		plain := o
		plain.AppliesTo = ""
		if got := Applicable(plain, changed); got != listed {
			t.Fatalf("unrestricted objective Applicable = %v, want %v; the Fix flag must not affect it", got, listed)
		}
		inputs := o
		inputs.AppliesTo = "inputs"
		listedInputs := false
		for i := range changed {
			changed[i].Inputs = rapid.Bool().Draw(t, "inputs")
			listedInputs = listedInputs || (changed[i].Inputs && o.Levels[changed[i].Level] != "")
		}
		if got := Applicable(inputs, changed); got != listedInputs {
			t.Fatalf("inputs Applicable = %v, want %v; an inputs objective must apply exactly through listed-level files that declare inputs, however many other files changed", got, listedInputs)
		}
	})
}

func TestContextListsFixObjectivesAsCommitsTypedFix(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	out := RenderContext(&Manifest{DefaultLevel: "B", Catalog: c}, nil)
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "VER-FAIL-ON-BASE") {
			line = l
		}
	}
	if !strings.Contains(line, "required") || !strings.Contains(line, "(commits typed fix)") || strings.Contains(line, "Assure-Kind") {
		t.Fatalf("context line for VER-FAIL-ON-BASE = %q; it must name the trigger, commits typed fix, and not the retired trailer, or an agent cannot tell which commits need a failing-on-base test\n%s", line, out)
	}
}

func TestReferencesListOneSortedCommandPerLanguage(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		refs := rapid.SliceOfDistinct(rapid.StringMatching(`[a-z][a-z0-9_-]{0,15}`), rapid.ID).Draw(t, "refs")
		out := RenderReferences(slices.Clone(refs))
		if len(refs) == 0 {
			if out != "" {
				t.Fatalf("no references rendered %q; context must not advertise a section with nothing in it", out)
			}
			return
		}
		var cmds []string
		for _, l := range strings.Split(out, "\n") {
			if _, cmd, ok := strings.Cut(l, "assure reference "); ok {
				cmds = append(cmds, cmd)
			}
		}
		if want := slices.Sorted(slices.Values(refs)); !slices.Equal(cmds, want) {
			t.Fatalf("commands for %v = %v, want one `assure reference <lang>` per reference in sorted order, so output is byte-identical\n%s", refs, cmds, out)
		}
	})
}
