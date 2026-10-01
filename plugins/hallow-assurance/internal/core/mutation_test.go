package core

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var mutation = Objective{ID: "VER-MUTATION-CHANGED", Evidence: "mutation.report", Levels: map[Level]string{"A": "required", "B": "required", "C": "advisory"}, Threshold: map[Level]float64{"A": 80, "B": 65, "C": 50}}

func mutants(path string, level Level, statuses map[string]int) []Mutant {
	var out []Mutant
	line := 1
	for _, s := range []string{"Killed", "Timeout", "Survived", "NoCoverage", "CompileError", "RuntimeError", "Ignored", "Pending"} {
		for range statuses[s] {
			out = append(out, Mutant{Path: path, Level: level, Line: line, Mutator: "CONDITIONALS_BOUNDARY", Status: s})
			line++
		}
	}
	return out
}

func decideMutation(o Objective, ms []Mutant, ws []Waiver) Outcome {
	var changed []ChangedFile
	for _, m := range ms {
		changed = append(changed, ChangedFile{Path: m.Path, Level: m.Level})
	}
	if len(changed) == 0 {
		changed = []ChangedFile{{Path: "a.go", Level: "B"}}
	}
	return DecideObjective(o, changed, Evidence{Mutation: true, Mutants: ms}, ws, nil, "2026-10-01")
}

func TestMutationTimeoutCountsAsDetected(t *testing.T) {
	got := decideMutation(mutation, mutants("a.go", "B", map[string]int{"Killed": 1, "Timeout": 1}), nil)
	if got.Status != Pass {
		t.Fatalf("got %+v; a mutant that hangs the tests is caught, as Stryker scores it", got)
	}
}

func TestMutationPendingFailsClosed(t *testing.T) {
	got := decideMutation(mutation, mutants("internal/x.go", "B", map[string]int{"Killed": 5, "Pending": 1}), nil)
	if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "internal/x.go:6") {
		t.Fatalf("got %+v; a mutant that never ran proves nothing and must fail, naming the mutant", got)
	}
}

func TestMutationThresholdIsAFloor(t *testing.T) {
	at := decideMutation(mutation, mutants("a.go", "B", map[string]int{"Killed": 13, "Survived": 7}), nil)
	below := decideMutation(mutation, mutants("a.go", "B", map[string]int{"Killed": 12, "Survived": 8}), nil)
	if at.Status != Pass || below.Status != Fail {
		t.Fatalf("at 65%%: %v, at 60%%: %v; the level-B floor is 65", at.Status, below.Status)
	}
}

func TestMutationInvalidMutantsDoNotCount(t *testing.T) {
	got := decideMutation(mutation, mutants("a.go", "B", map[string]int{"Killed": 13, "NoCoverage": 7, "CompileError": 30, "RuntimeError": 2, "Ignored": 4}), nil)
	if got.Status != Pass {
		t.Fatalf("got %+v; compile errors are not tests' fault and must not dilute the score", got)
	}
}

func TestMutationNoMutantsPasses(t *testing.T) {
	for _, ms := range [][]Mutant{nil, mutants("a.go", "B", map[string]int{"CompileError": 3, "Ignored": 1})} {
		if got := decideMutation(mutation, ms, nil); got.Status != Pass {
			t.Fatalf("got %+v; a change with no valid mutants has nothing to test", got)
		}
	}
}

func TestMutationLevelsAreScoredSeparately(t *testing.T) {
	ms := slices.Concat(mutants("b.go", "B", map[string]int{"Killed": 4}), mutants("c.go", "C", map[string]int{"Killed": 2, "Survived": 3}))
	if got := decideMutation(mutation, ms, nil); got.Status != AdvisoryFail {
		t.Fatalf("got %+v; level C at 40%% is advisory and level B passes", got)
	}
}

func TestMutationWaiverCoversWeakFile(t *testing.T) {
	ws := mustWaivers(t, waiverYAML("VER-MUTATION-CHANGED", "internal/legacy/**", "2026-12-31"))
	ms := slices.Concat(mutants("internal/legacy/a.go", "B", map[string]int{"Survived": 3}), mutants("b.go", "B", map[string]int{"Killed": 2}))
	got := decideMutation(mutation, ms, ws)
	if got.Status != Waived || len(got.Waivers) != 1 {
		t.Fatalf("got %+v; the waiver removed the only failure, so the objective is waived with that waiver listed", got)
	}
}

func TestMutationDetailsNameSurvivors(t *testing.T) {
	ms := []Mutant{{Path: "internal/app/check.go", Level: "B", Line: 42, Mutator: "CONDITIONALS_BOUNDARY", Status: "Survived"}}
	got := decideMutation(mutation, ms, nil)
	d := strings.Join(got.Details, "\n")
	for _, want := range []string{"internal/app/check.go:42", "CONDITIONALS_BOUNDARY", "Survived", "0 of 1", "threshold 65"} {
		if !strings.Contains(d, want) {
			t.Fatalf("details %q lack %q; the reader must be able to find the survivor", d, want)
		}
	}
}

func TestMutationNoThresholdNeedsEveryMutant(t *testing.T) {
	o := mutation
	o.Threshold = map[Level]float64{"A": 80}
	if got := decideMutation(o, mutants("a.go", "B", map[string]int{"Killed": 99, "Survived": 1}), nil); got.Status != Fail {
		t.Fatalf("got %+v; with no threshold every mutant must be detected", got)
	}
}

type mutationWorld struct {
	o       Objective
	ms      []Mutant
	ws      []Waiver
	changed []ChangedFile
}

func mutationWorldGen(t *rapid.T) mutationWorld {
	statuses := []string{"Killed", "Timeout", "Survived", "NoCoverage", "CompileError", "RuntimeError", "Ignored"}
	paths := []string{"legacy/a.go", "legacy/b.go", "app/c.go", "app/d.go"}
	w := mutationWorld{o: Objective{ID: "VER-MUTATION-CHANGED", Levels: map[Level]string{}, Threshold: map[Level]float64{}}}
	for _, l := range levels {
		if s := rapid.SampledFrom([]string{"", "required", "advisory"}).Draw(t, "status"+string(l)); s != "" {
			w.o.Levels[l] = s
			if rapid.Bool().Draw(t, "th"+string(l)) {
				w.o.Threshold[l] = float64(rapid.IntRange(0, 100).Draw(t, "thv"+string(l)))
			}
		}
	}
	levelOf := map[string]Level{}
	for _, p := range paths {
		levelOf[p] = levelGen.Draw(t, "level "+p)
		w.changed = append(w.changed, ChangedFile{Path: p, Level: levelOf[p]})
	}
	for i := range rapid.IntRange(0, 12).Draw(t, "n") {
		p := rapid.SampledFrom(paths).Draw(t, "path")
		w.ms = append(w.ms, Mutant{Path: p, Level: levelOf[p], Line: i + 1, Mutator: "M", Status: rapid.SampledFrom(statuses).Draw(t, "st")})
	}
	if rapid.Bool().Draw(t, "waiver") {
		w.ws = mustWaivers(t, waiverYAML("VER-MUTATION-CHANGED", "legacy/**", rapid.SampledFrom([]string{"2026-09-01", "2026-12-31"}).Draw(t, "expires")))
	}
	return w
}

func (w mutationWorld) waived(p string) bool {
	return len(w.ws) > 0 && w.ws[0].Expires >= "2026-10-01" && strings.HasPrefix(p, "legacy/")
}

func (w mutationWorld) levelFails(l Level, skipWaived bool) bool {
	det, und := 0, 0
	for _, m := range w.ms {
		if m.Level != l || (skipWaived && w.waived(m.Path)) {
			continue
		}
		switch m.Status {
		case "Killed", "Timeout":
			det++
		case "Survived", "NoCoverage":
			und++
		}
	}
	if det+und == 0 {
		return false
	}
	th, ok := w.o.Threshold[l]
	if !ok {
		return und > 0
	}
	return big.NewRat(int64(100*det), int64(det+und)).Cmp(new(big.Rat).SetFloat64(th)) < 0
}

func (w mutationWorld) oracle() Status {
	want := Pass
	for _, l := range levels {
		if w.o.Levels[l] == "" {
			continue
		}
		switch {
		case w.levelFails(l, true) && w.o.Levels[l] == "required":
			want = Fail
		case w.levelFails(l, true) && want != Fail:
			want = AdvisoryFail
		case w.levelFails(l, false) && want == Pass:
			want = Waived
		}
	}
	return want
}

func TestMutationDecisionMatchesOracle(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := mutationWorldGen(t)
		if !Applicable(w.o, w.changed) {
			return
		}
		got := DecideObjective(w.o, w.changed, Evidence{Mutation: true, Mutants: w.ms}, w.ws, nil, "2026-10-01")
		if want := w.oracle(); got.Status != want {
			t.Fatalf("got %v, want %v\nobjective %+v\nmutants %+v\nwaivers %+v\ndetails %s", got.Status, want, w.o, w.ms, w.ws, got.Details)
		}
		rev := slices.Clone(w.ms)
		slices.Reverse(rev)
		if again := DecideObjective(w.o, w.changed, Evidence{Mutation: true, Mutants: rev}, w.ws, nil, "2026-10-01"); again.Status != got.Status || fmt.Sprint(again.Details) != fmt.Sprint(got.Details) {
			t.Fatalf("result depends on mutant order: %v %v vs %v %v", got.Status, got.Details, again.Status, again.Details)
		}
	})
}
