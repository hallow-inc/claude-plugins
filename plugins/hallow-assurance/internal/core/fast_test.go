package core

import (
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
	for range rapid.IntRange(0, 2).Draw(t, "nfailing") {
		ev.Failing = append(ev.Failing, "pkg.TestX")
	}
	for range rapid.IntRange(0, 3).Draw(t, "nfindings") {
		ev.Findings = append(ev.Findings, Finding{Level: levelGen.Draw(t, "flevel"), Located: rapid.Bool().Draw(t, "located"), Text: "finding"})
	}
	return ev
}

func rank(s Status) int { return map[Status]int{Pass: 0, AdvisoryFail: 1, Fail: 2}[s] }

func oracle(o Objective, changed []Level, ev Evidence) Status {
	statuses := map[string]bool{}
	for _, l := range changed {
		statuses[o.Levels[l]] = true
	}
	unlocated := len(ev.Problems)+len(ev.Failing) > 0
	requiredHit, advisoryHit := false, false
	for _, f := range ev.Findings {
		if !f.Located {
			unlocated = true
		} else if o.Levels[f.Level] == "required" {
			requiredHit = true
		} else if o.Levels[f.Level] == "advisory" {
			advisoryHit = true
		}
	}
	if statuses["required"] && (unlocated || requiredHit) {
		return Fail
	}
	if requiredHit || advisoryHit || (unlocated && statuses["advisory"]) {
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
		rc, rf := slices.Clone(changed), slices.Clone(ev.Findings)
		slices.Reverse(rc)
		slices.Reverse(rf)
		if DecideFast(o, changed, ev).Status != DecideFast(o, rc, Evidence{ev.Problems, ev.Failing, rf}).Status {
			t.Fatal("status depends on input order")
		}
	})
}

func TestMoreEvidenceNeverImprovesTheStatus(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o := objectiveGen(t)
		changed := rapid.SliceOfN(levelGen, 1, 4).Draw(t, "changed")
		ev, extra := evidenceGen(t), evidenceGen(t)
		more := Evidence{append(slices.Clone(ev.Problems), extra.Problems...), append(slices.Clone(ev.Failing), extra.Failing...), append(slices.Clone(ev.Findings), extra.Findings...)}
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

func TestLintFindingAtLevelDIsAdvisory(t *testing.T) {
	o := Objective{ID: "CODE-ZERO-WARNINGS", Levels: map[Level]string{"A": "required", "B": "required", "C": "required", "D": "advisory"}}
	got := DecideFast(o, []Level{"D"}, Evidence{Findings: []Finding{{Level: "D", Located: true, Text: "x.go: unused"}}})
	if got.Status != AdvisoryFail {
		t.Fatalf("got %+v", got)
	}
}
