package core

import (
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func flakyFailure(t interface{ Fatalf(string, ...any) }, name, text string, rerun int) TestFailure {
	f := TestFailure{Name: name, Text: text}
	v := reflect.ValueOf(&f).Elem().FieldByName("Flaky")
	if !v.IsValid() || v.Kind() != reflect.Int {
		t.Fatalf("core.TestFailure has no `Flaky int` field, so a flaky test cannot be told apart from one the agent broke")
	}
	v.SetInt(int64(rerun))
	return f
}

func TestFlakyTestsDoNotChangeTheFingerprint(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := fpInputGen(t)
		before := in.fingerprint()
		withFlaky := fpInput{ids: slices.Clone(in.ids), evs: slices.Clone(in.evs)}
		for i, ev := range withFlaky.evs {
			added := slices.Clone(ev.Failing)
			for range rapid.IntRange(0, 2).Draw(t, "flaky") {
				added = append(added, flakyFailure(t, "F"+fpName.Draw(t, "flaky test"), fpText.Draw(t, "flaky text"), rapid.IntRange(1, 3).Draw(t, "rerun")))
			}
			withFlaky.evs[i].Failing = perm(t, added, "order")
			if got, want := withFlaky.evs[i].Keys(), ev.Keys(); !slices.Equal(got, want) {
				t.Fatalf("flaky tests changed the keys from %q to %q; a test that passed on rerun is not the agent's to fix, so it must not open a new block budget", want, got)
			}
		}
		if withFlaky.fingerprint() != before {
			t.Fatal("adding flaky tests changed the stop fingerprint, so a test that failed then passed on rerun would block the agent again")
		}
	})
}
