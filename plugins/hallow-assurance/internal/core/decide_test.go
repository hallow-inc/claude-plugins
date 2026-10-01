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

var levelB = Objective{ID: "CODE-NO-UNSAFE", Evidence: "lint.sarif", Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"}}

func TestScopedWaiverSuppressesALocatedFinding(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("CODE-NO-UNSAFE", "internal/decode/**", "2026-12-31"))
	ev := Evidence{Findings: []Finding{{Level: "B", Located: true, Path: "internal/decode/decode.go", Rule: "import-unsafe", Text: "internal/decode/decode.go: import-unsafe"}}}
	got := DecideObjective(levelB, []ChangedFile{{Path: "internal/decode/decode.go", Level: "B"}}, ev, ws, nil, "2026-09-27")
	if got.Status != Waived || len(got.Waivers) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestPartialScopeDoesNotCoverMissingEvidence(t *testing.T) {
	o := Objective{ID: "IND-VERIFIER-DISTINCT", Levels: map[Level]string{"A": "required", "B": "required"}}
	ws := mustWaivers(t, waiverYAML("IND-VERIFIER-DISTINCT", "internal/**", "2026-12-31"))
	changed := []ChangedFile{{Path: "internal/core/fast.go", Level: "B"}, {Path: "cmd/assure/main.go", Level: "B"}}
	got := DecideObjective(o, changed, Evidence{Problems: []string{"no adapter lists IND-VERIFIER-DISTINCT"}}, ws, nil, "2026-09-27")
	if got.Status != Fail {
		t.Fatalf("got %+v; a narrow waiver must not hide a failure that also covers cmd/", got)
	}
}

func TestRepoWideWaiverCoversMissingEvidence(t *testing.T) {
	o := Objective{ID: "IND-VERIFIER-DISTINCT", Levels: map[Level]string{"A": "required", "B": "required"}}
	ws := mustWaivers(t, waiverYAML("IND-VERIFIER-DISTINCT", "**", "2026-12-31"))
	changed := []ChangedFile{{Path: "internal/core/fast.go", Level: "B"}, {Path: "cmd/assure/main.go", Level: "B"}}
	got := DecideObjective(o, changed, Evidence{Problems: []string{"no adapter lists IND-VERIFIER-DISTINCT"}}, ws, nil, "2026-09-27")
	if got.Status != Waived {
		t.Fatalf("got %+v", got)
	}
}

func TestWaiverIsActiveOnItsLastDayOnly(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("CODE-NO-UNSAFE", "**", "2026-01-02"))
	ev := Evidence{Findings: []Finding{{Level: "B", Located: true, Path: "a.go", Text: "a.go: import-unsafe"}}}
	changed := []ChangedFile{{Path: "a.go", Level: "B"}}
	if got := DecideObjective(levelB, changed, ev, ws, nil, "2026-01-02"); got.Status != Waived {
		t.Fatalf("on the expiry date: got %s, want waived", got.Status)
	}
	if got := DecideObjective(levelB, changed, ev, ws, nil, "2026-01-03"); got.Status != Fail || len(got.Waivers) != 0 {
		t.Fatalf("after expiry: got %+v; an expired waiver must suppress nothing", got)
	}
	if exp := Expired(ws, "2026-01-03"); len(exp) != 1 {
		t.Fatalf("Expired = %v", exp)
	}
}

func TestWaiverForAnotherObjectiveDoesNothing(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("CODE-ZERO-WARNINGS", "**", "2026-12-31"))
	ev := Evidence{Findings: []Finding{{Level: "B", Located: true, Path: "a.go", Text: "a.go: import-unsafe"}}}
	if got := DecideObjective(levelB, []ChangedFile{{Path: "a.go", Level: "B"}}, ev, ws, nil, "2026-09-27"); got.Status != Fail {
		t.Fatalf("got %s", got.Status)
	}
}

var complexity = Objective{ID: "CODE-COMPLEXITY", Evidence: "lint.sarif", Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"}, Threshold: map[Level]float64{"A": 15, "B": 15, "C": 15}}

func TestMetricAtThresholdPasses(t *testing.T) {
	ev := Evidence{Findings: []Finding{{Level: "B", Located: true, Path: "a.go", Rule: "cyclomatic", Metric: 15, HasMetric: true, Text: "a.go: cyclomatic 15"}}}
	got := DecideObjective(complexity, []ChangedFile{{Path: "a.go", Level: "B"}}, ev, nil, nil, "2026-09-27")
	if got.Status != Pass || got.UnderThreshold != 1 {
		t.Fatalf("got %+v; thresholds are ceilings, so 15 <= 15 passes", got)
	}
}

func TestMetricOverThresholdFails(t *testing.T) {
	ev := Evidence{Findings: []Finding{{Level: "B", Located: true, Path: "a.go", Rule: "cyclomatic", Metric: 16, HasMetric: true, Text: "a.go: cyclomatic 16"}}}
	got := DecideObjective(complexity, []ChangedFile{{Path: "a.go", Level: "B"}}, ev, nil, nil, "2026-09-27")
	if got.Status != Fail || !slices.Contains(got.Details, "a.go: cyclomatic 16") {
		t.Fatalf("got %+v", got)
	}
}

func TestMetricWithNoThresholdAtItsLevelCounts(t *testing.T) {
	o := complexity
	o.Threshold = map[Level]float64{"A": 15}
	ev := Evidence{Findings: []Finding{{Level: "B", Located: true, Path: "a.go", Metric: 1, HasMetric: true, Text: "a.go: 1"}}}
	if got := DecideObjective(o, []ChangedFile{{Path: "a.go", Level: "B"}}, ev, nil, nil, "2026-09-27"); got.Status != Fail {
		t.Fatalf("got %s", got.Status)
	}
}

func TestLevelDSkipsLevelBOnlyObjectives(t *testing.T) {
	if Applicable(complexity, []ChangedFile{{Path: "a.go", Level: "D"}}) {
		t.Fatal("CODE-COMPLEXITY has no level D entry but was applicable")
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
	got := DecideObjective(o, []ChangedFile{{Path: "a.go", Level: "B"}}, Evidence{Problems: []string{"no adapter lists IND-VERIFIER-DISTINCT"}}, nil, nil, "2026-09-27")
	if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "no adapter lists") {
		t.Fatalf("got %+v; missing evidence must fail closed", got)
	}
}

type world struct {
	o        Objective
	changed  []ChangedFile
	levelOf  map[string]Level
	ev       Evidence
	ws       []Waiver
	baseline map[Fingerprint]int
	date     string
}

var (
	paths  = []string{"a/x.go", "a/y.go", "b/z.go"}
	scopes = []string{"**", "a/**", "b/**", "a/x.go"}
	dates  = []string{"2026-01-01", "2026-01-02", "2026-01-03"}
)

func worldGen(t *rapid.T) world {
	w := world{o: objectiveGen(t), levelOf: map[string]Level{}, date: "2026-01-02"}
	w.o.ID = "CODE-X"
	if rapid.Bool().Draw(t, "hasth") {
		w.o.Threshold = map[Level]float64{}
		for _, l := range levels {
			if w.o.Levels[l] != "" && rapid.Bool().Draw(t, "th"+string(l)) {
				w.o.Threshold[l] = 10
			}
		}
	}
	for _, p := range paths {
		w.levelOf[p] = levelGen.Draw(t, "level "+p)
	}
	for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(paths), 1, 3, func(s string) string { return s }).Draw(t, "changed") {
		w.changed = append(w.changed, ChangedFile{Path: p, Level: w.levelOf[p]})
	}
	for range rapid.IntRange(0, 1).Draw(t, "nproblems") {
		w.ev.Problems = append(w.ev.Problems, "no evidence")
	}
	for range rapid.IntRange(0, 4).Draw(t, "nfindings") {
		f := Finding{Rule: rapid.SampledFrom([]string{"r1", "r2"}).Draw(t, "rule"), Text: "finding"}
		if rapid.Bool().Draw(t, "located") {
			f.Located, f.Path = true, rapid.SampledFrom(paths).Draw(t, "fpath")
			f.Level = w.levelOf[f.Path]
		}
		if rapid.Bool().Draw(t, "metric") {
			f.Metric, f.HasMetric = float64(rapid.IntRange(8, 12).Draw(t, "m")), true
		}
		w.ev.Findings = append(w.ev.Findings, f)
	}
	var src strings.Builder
	for range rapid.IntRange(0, 2).Draw(t, "nwaivers") {
		obj := rapid.SampledFrom([]string{"CODE-X", "CODE-Y"}).Draw(t, "wobj")
		src.WriteString(waiverYAML(obj, rapid.SampledFrom(scopes).Draw(t, "scope"), rapid.SampledFrom(dates).Draw(t, "expires")))
	}
	if src.Len() > 0 {
		w.ws = mustWaivers(t, src.String())
	}
	w.baseline = map[Fingerprint]int{}
	for range rapid.IntRange(0, 2).Draw(t, "nbase") {
		w.baseline[Fingerprint{"CODE-X", rapid.SampledFrom([]string{"r1", "r2"}).Draw(t, "brule"), rapid.SampledFrom(paths).Draw(t, "bpath"), ""}] = rapid.IntRange(1, 2).Draw(t, "bcount")
	}
	return w
}

func (w world) decide() Outcome {
	return DecideObjective(w.o, w.changed, w.ev, w.ws, w.baseline, w.date)
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

func (w world) oracleCounted() (unlocated int, matches map[Fingerprint]int) {
	unlocated, matches = len(w.ev.Problems), map[Fingerprint]int{}
	for _, f := range w.ev.Findings {
		th, hasTh := w.o.Threshold[f.Level]
		switch {
		case !f.Located:
			unlocated++
		case !(hasTh && f.HasMetric && f.Metric <= th):
			matches[Fingerprint{"CODE-X", f.Rule, f.Path, f.Message}]++
		}
	}
	return unlocated, matches
}

func evalOracle(w world) Status {
	active := w.oracleActive()
	req, adv, applicable := w.oracleApplicable()
	unlocated, matches := w.oracleCounted()
	blocking, advisory, waived := false, false, false
	if unlocated > 0 && (req || adv) {
		waived = oracleCovered(active, applicable)
		blocking, advisory = req && !waived, !waived
	}
	for fp, n := range matches {
		status := w.o.Levels[w.levelOf[fp.Path]]
		if n <= w.baseline[fp] || status == "" {
			continue
		}
		if slices.ContainsFunc(active, func(x Waiver) bool { return globMatch(x.Scope, fp.Path) }) {
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
		w.ev = Evidence{Problems: []string{"no adapter lists CODE-X"}}
		req, adv, applicable := changedStatus(w.o, w.changed)
		if !req && !adv {
			t.Skip("not applicable")
		}
		covered := slices.ContainsFunc(w.ws, func(x Waiver) bool {
			return x.Objective == "CODE-X" && x.Expires >= w.date && !slices.ContainsFunc(applicable, func(p string) bool { return !globMatch(x.Scope, p) })
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
		withoutWaivers := DecideObjective(w.o, w.changed, w.ev, nil, w.baseline, w.date)
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
		r.ev.Findings = slices.Clone(w.ev.Findings)
		slices.Reverse(r.ev.Findings)
		r.ws = slices.Clone(w.ws)
		slices.Reverse(r.ws)
		b := r.decide()
		if a.Status != b.Status || !slices.Equal(a.Details, b.Details) || a.Baselined != b.Baselined || a.UnderThreshold != b.UnderThreshold ||
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

func TestContextListsFixObjectivesWithTheTrailer(t *testing.T) {
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
	if !strings.Contains(line, "required") || !strings.Contains(line, "Assure-Kind: fix") {
		t.Fatalf("context line for VER-FAIL-ON-BASE = %q; an agent that never sees the trailer cannot know a fix needs a failing-on-base test\n%s", line, out)
	}
}
