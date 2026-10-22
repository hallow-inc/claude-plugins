package core

import (
	"fmt"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

var levelGen = rapid.SampledFrom(levels)

func objectiveGen(t *rapid.T) Objective {
	o := Objective{ID: "VER-X", Levels: map[Level]string{}}
	for _, l := range levels {
		if s := rapid.SampledFrom([]string{"", "required", "advisory"}).Draw(t, "status"+string(l)); s != "" {
			o.Levels[l] = s
		}
	}
	return o
}

func evidenceGen(t *rapid.T) Evidence {
	var ev Evidence
	for range rapid.IntRange(0, 1).Draw(t, "nproblems") {
		ev.Problems = append(ev.Problems, "missing evidence")
	}
	for i := range rapid.IntRange(0, 2).Draw(t, "nfailing") {
		name := fmt.Sprintf("pkg.Test%d", i)
		ev.Failing = append(ev.Failing, TestFailure{Name: name, Text: "failing test: " + name})
	}
	return ev
}

func rank(s Status) int { return map[Status]int{Pass: 0, AdvisoryFail: 1, Fail: 2}[s] }

func oracle(o Objective, changed []Level, ev Evidence) Status {
	statuses := map[string]bool{}
	for _, l := range changed {
		statuses[o.Levels[l]] = true
	}
	failed := len(ev.Problems)+len(ev.Failing) > 0
	switch {
	case failed && statuses["required"]:
		return Fail
	case failed && statuses["advisory"]:
		return AdvisoryFail
	}
	return Pass
}

func TestDecideFastMatchesTheSpecRules(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o := objectiveGen(t)
		changed := rapid.SliceOfN(levelGen, 1, 4).Draw(t, "changed")
		ev := evidenceGen(t)
		if got, want := DecideFast(o, changed, ev).Status, oracle(o, changed, ev); got != want {
			t.Fatalf("DecideFast = %s, spec says %s (levels %v, changed %v, evidence %+v)", got, want, o.Levels, changed, ev)
		}
	})
}

func TestDecideFastIgnoresInputOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o := objectiveGen(t)
		changed := rapid.SliceOfN(levelGen, 1, 4).Draw(t, "changed")
		ev := evidenceGen(t)
		rc, rf := slices.Clone(changed), slices.Clone(ev.Failing)
		slices.Reverse(rc)
		slices.Reverse(rf)
		a, b := DecideFast(o, changed, ev), DecideFast(o, rc, Evidence{Problems: ev.Problems, Failing: rf})
		if a.Status != b.Status || !slices.Equal(slices.Sorted(slices.Values(a.Details)), slices.Sorted(slices.Values(b.Details))) {
			t.Fatalf("outcome depends on input order: %+v vs %+v", a, b)
		}
	})
}

func TestMoreEvidenceNeverImprovesTheStatus(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o := objectiveGen(t)
		changed := rapid.SliceOfN(levelGen, 1, 4).Draw(t, "changed")
		ev, extra := evidenceGen(t), evidenceGen(t)
		more := Evidence{Problems: append(slices.Clone(ev.Problems), extra.Problems...), Failing: append(slices.Clone(ev.Failing), extra.Failing...)}
		if rank(DecideFast(o, changed, more).Status) < rank(DecideFast(o, changed, ev).Status) {
			t.Fatal("adding evidence of failure improved the status")
		}
	})
}

func TestMissingEvidenceFails(t *testing.T) {
	o := Objective{ID: "VER-TESTS-PASS", Levels: map[Level]string{"A": "required", "B": "required", "C": "required", "D": "required"}}
	if got := DecideFast(o, []Level{"D"}, Evidence{Problems: []string{"no test.junit evidence"}}); got.Status != Fail {
		t.Fatalf("got %+v", got)
	}
}

func TestChangedFileCarriesItsComponentsFlags(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := &Manifest{DefaultLevel: levelGen.Draw(t, "default")}
		for i := range rapid.IntRange(0, 4).Draw(t, "ncomp") {
			m.Components = append(m.Components, Component{
				Glob: mustGlob(t, fmt.Sprintf("c%d/**", i)), Level: levelGen.Draw(t, "level"),
				Formal: rapid.Bool().Draw(t, "formal"), Inputs: rapid.Bool().Draw(t, "inputs"),
			})
		}
		want := ChangedFile{Path: "other/x.go", Level: m.DefaultLevel}
		if i := rapid.IntRange(-1, len(m.Components)-1).Draw(t, "component"); i >= 0 {
			c := m.Components[i]
			want = ChangedFile{Path: fmt.Sprintf("c%d/x.go", i), Level: c.Level, Formal: c.Formal, Inputs: c.Inputs}
		}
		got := m.ChangedFile(want.Path)
		if got.Path != want.Path || got.Level != want.Level || got.Formal != want.Formal || got.Inputs != want.Inputs {
			t.Fatalf("ChangedFile(%q) = %+v, want %+v; formal and inputs objectives apply only to files of components that declare them", want.Path, got, want)
		}
	})
}

func TestFailingTestAtAnAdvisoryLevelDoesNotBlock(t *testing.T) {
	o := Objective{ID: "VER-TESTS-PASS", Levels: map[Level]string{"A": "required", "B": "required", "C": "required", "D": "advisory"}}
	ev := Evidence{Failing: []TestFailure{{Name: "pkg.TestX", Text: "failing test: pkg.TestX"}}}
	got := DecideFast(o, []Level{"D"}, ev)
	if got.Status != AdvisoryFail || !slices.Contains(got.Details, "failing test: pkg.TestX") {
		t.Fatalf("got %+v; a failure where the objective is advisory is reported, named, and does not block", got)
	}
	if got := DecideFast(o, []Level{"D", "B"}, ev); got.Status != Fail {
		t.Fatalf("got %+v; the same failure blocks once a changed file sits at a required level", got)
	}
}
