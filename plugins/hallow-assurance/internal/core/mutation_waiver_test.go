package core

import (
	"path"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func TestMutationWaivingAWellTestedFileDoesNotFailTheLevel(t *testing.T) {
	o := Objective{ID: "VER-MUTATION-CHANGED", Levels: map[Level]string{"A": "required"}, Threshold: map[Level]float64{"A": 80}}
	changed := []ChangedFile{{Path: "a.go", Level: "A"}, {Path: "b.go", Level: "A"}}
	ms := slices.Concat(mutants("a.go", "A", map[string]int{"Killed": 4}), mutants("b.go", "A", map[string]int{"Survived": 1}))
	ev := Evidence{Mutation: true, Mutants: ms}
	ws := mustWaivers(t, waiverYAML("VER-MUTATION-CHANGED", "a.go", "2099-01-01"))

	without := DecideObjective(o, changed, ev, nil, "2026-11-03")
	got := DecideObjective(o, changed, ev, ws, "2026-11-03")

	if without.Status != Pass || got.Status != Pass {
		t.Fatalf("without waiver %v, with waiver on a.go %v (%v); level A scores 4 of 5 = 80%%, and a waiver excuses only a.go's undetected mutants, so its killed mutants still count and the level passes", without.Status, got.Status, got.Details)
	}
}

func oneScoredLevelWorldGen(t *rapid.T) mutationWorld {
	statuses := []string{"Killed", "Timeout", "Survived", "NoCoverage", "CompileError", "Ignored"}
	l := levelGen.Draw(t, "level")
	w := mutationWorld{o: Objective{ID: "VER-MUTATION-CHANGED", Levels: map[Level]string{l: rapid.SampledFrom([]string{"required", "advisory"}).Draw(t, "status")}, Threshold: map[Level]float64{}}}
	if rapid.IntRange(0, 9).Draw(t, "has threshold") > 0 {
		w.o.Threshold[l] = float64(rapid.IntRange(0, 100).Draw(t, "threshold"))
	}
	for _, p := range []string{"legacy/a.go", "legacy/b.go", "app/c.go", "app/d.go"} {
		w.changed = append(w.changed, ChangedFile{Path: p, Level: l})
		for range rapid.IntRange(0, 5).Draw(t, "n "+p) {
			w.ms = append(w.ms, Mutant{Path: p, Level: l, Line: len(w.ms) + 1, Mutator: "M", Status: rapid.SampledFrom(statuses).Draw(t, "st")})
		}
	}
	if rapid.Bool().Draw(t, "waiver") {
		w.ws = mustWaivers(t, waiverYAML("VER-MUTATION-CHANGED", "legacy/**", rapid.SampledFrom([]string{"2026-09-01", "2026-12-31"}).Draw(t, "expires")))
	}
	return w
}

func scopeOverADetectedMutantGen(t *rapid.T, ms []Mutant) string {
	scopes := []string{"**"}
	for _, m := range ms {
		if _, killed := ScoredMutant(m.Status); killed {
			scopes = append(scopes, m.Path, path.Dir(m.Path)+"/**")
		}
	}
	return rapid.SampledFrom(scopes).Draw(t, "extra scope")
}

func TestMutationAddingAWaiverNeverWorsensTheStatus(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := oneScoredLevelWorldGen(t)
		if !Applicable(w.o, w.changed) {
			return
		}
		scope := scopeOverADetectedMutantGen(t, w.ms)
		more := append(append([]Waiver{}, w.ws...), mustWaivers(t, waiverYAML("VER-MUTATION-CHANGED", scope, "2099-01-01"))...)
		ev := Evidence{Mutation: true, Mutants: w.ms}

		before := DecideObjective(w.o, w.changed, ev, w.ws, "2026-10-01")
		after := DecideObjective(w.o, w.changed, ev, more, "2026-10-01")

		if rank(after.Status) > rank(before.Status) {
			t.Fatalf("adding an active waiver on %q turned %v into %v\nobjective %+v\nmutants %+v\ndetails before %v\ndetails after %v\na waiver may only excuse undetected mutants; dropping a waived file's killed mutants lowers the score and punishes the author for waiving", scope, before.Status, after.Status, w.o, w.ms, before.Details, after.Details)
		}
	})
}
