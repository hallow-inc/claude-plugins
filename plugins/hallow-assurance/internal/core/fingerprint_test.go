package core

import (
	"slices"
	"testing"

	"pgregory.net/rapid"
)

var (
	fpName = rapid.StringMatching(`[a-c]{1,2}`)
	fpText = rapid.StringMatching(`[ -~]{0,12}`)
)

func fpEvidence(t *rapid.T) Evidence {
	var ev Evidence
	for range rapid.IntRange(0, 2).Draw(t, "problems") {
		ev.Problems = append(ev.Problems, fpText.Draw(t, "problem"))
	}
	for range rapid.IntRange(0, 3).Draw(t, "failing") {
		ev.Failing = append(ev.Failing, TestFailure{Name: "T" + fpName.Draw(t, "test"), Text: fpText.Draw(t, "text")})
	}
	for range rapid.IntRange(0, 3).Draw(t, "findings") {
		f := Finding{Rule: fpName.Draw(t, "rule"), Message: fpText.Draw(t, "msg"), Text: fpText.Draw(t, "ftext")}
		if rapid.Bool().Draw(t, "located") {
			f.Located, f.Path = true, fpName.Draw(t, "path")+".x"
		}
		ev.Findings = append(ev.Findings, f)
	}
	for range rapid.IntRange(0, 2).Draw(t, "mutants") {
		ev.Mutants = append(ev.Mutants, Mutant{Path: fpName.Draw(t, "mpath"), Line: rapid.IntRange(1, 9).Draw(t, "line"),
			Mutator: fpName.Draw(t, "mutator"), Status: rapid.SampledFrom([]string{"Survived", "NoCoverage", "Pending"}).Draw(t, "status")})
	}
	return ev
}

type fpInput struct {
	ids     []string
	evs     []Evidence
	drifted []string
}

func fpInputGen(t *rapid.T) fpInput {
	var in fpInput
	in.ids = rapid.SliceOfNDistinct(rapid.StringMatching(`O[A-C]`), 0, 3, rapid.ID[string]).Draw(t, "objectives")
	for range in.ids {
		in.evs = append(in.evs, fpEvidence(t))
	}
	in.drifted = rapid.SliceOfN(fpName, 0, 3).Draw(t, "drifted")
	return in
}

func (in fpInput) fingerprint() string {
	fs := make([]FailureKey, len(in.ids))
	for i, id := range in.ids {
		fs[i] = FailureKey{Objective: id, Lang: "l", Keys: in.evs[i].Keys()}
	}
	return StopFingerprint(fs, in.drifted)
}

func perm[S ~[]E, E any](t *rapid.T, s S, label string) S {
	out := slices.Clone(s)
	for i := len(out) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, label)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func rewordEvidence(t *rapid.T, ev Evidence) Evidence {
	out := Evidence{Base: ev.Base}
	for range ev.Problems {
		out.Problems = append(out.Problems, fpText.Draw(t, "reworded problem"))
	}
	for _, f := range ev.Failing {
		out.Failing = append(out.Failing, TestFailure{Name: f.Name, Text: fpText.Draw(t, "reworded text")})
	}
	for _, f := range ev.Findings {
		f.Message, f.Text = fpText.Draw(t, "reworded msg"), fpText.Draw(t, "reworded ftext")
		out.Findings = append(out.Findings, f)
	}
	for _, m := range ev.Mutants {
		m.Line = rapid.IntRange(1, 9).Draw(t, "moved line")
		out.Mutants = append(out.Mutants, m)
	}
	out.Problems, out.Failing, out.Findings, out.Mutants = perm(t, out.Problems, "p"), perm(t, out.Failing, "f"), perm(t, out.Findings, "g"), perm(t, out.Mutants, "m")
	return out
}

func TestFingerprintIgnoresOrderAndMessageText(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := fpInputGen(t)
		order := make([]int, len(in.ids))
		for i := range order {
			order[i] = i
		}
		order = perm(t, order, "order")
		var re fpInput
		for _, i := range order {
			re.ids = append(re.ids, in.ids[i])
			re.evs = append(re.evs, rewordEvidence(t, in.evs[i]))
		}
		re.drifted = perm(t, in.drifted, "d")
		if in.fingerprint() != re.fingerprint() {
			t.Fatal("reordering inputs or rewording messages changed the fingerprint")
		}
	})
}

func TestFingerprintTracksFailureIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := fpInputGen(t)
		before := in.fingerprint()
		grown := fpInput{ids: slices.Clone(in.ids), evs: slices.Clone(in.evs), drifted: slices.Clone(in.drifted)}
		what := rapid.SampledFrom([]string{"objective", "test", "finding", "drifted"}).Draw(t, "what")
		switch what {
		case "objective":
			grown.ids = append(grown.ids, "NEW")
			grown.evs = append(grown.evs, Evidence{})
		case "drifted":
			grown.drifted = append(grown.drifted, "new-path")
		default:
			if len(in.ids) == 0 {
				t.Skip("no objective to extend")
			}
			i := rapid.IntRange(0, len(in.ids)-1).Draw(t, "which")
			ev := in.evs[i]
			if what == "test" {
				ev.Failing = append(slices.Clone(ev.Failing), TestFailure{Name: "TNew"})
			} else {
				ev.Findings = append(slices.Clone(ev.Findings), Finding{Located: true, Path: "new.x", Rule: "r"})
			}
			grown.evs[i] = ev
		}
		if grown.fingerprint() == before {
			t.Fatalf("adding a %s left the fingerprint unchanged", what)
		}
	})
}

func TestBaseRunKeysNameEachWayAFixFailsToProveItself(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		names := func(label string) []string {
			return rapid.SliceOfNDistinct(rapid.StringMatching(`[a-c]{1,3}`), 0, 2, rapid.ID[string]).Draw(t, label)
		}
		b := BaseRun{Failed: rapid.IntRange(0, 2).Draw(t, "failed"), Passed: names("passed"), Skipped: names("skipped"), Errored: names("errored")}
		var want []string
		if b.Failed+len(b.Passed)+len(b.Skipped)+len(b.Errored) == 0 {
			want = append(want, "base-no-test")
		}
		for _, n := range b.Passed {
			want = append(want, "base-pass:"+n)
		}
		for _, n := range b.Skipped {
			want = append(want, "base-skip:"+n)
		}
		if len(b.Errored) > 0 {
			want = append(want, "base-error")
		}
		slices.Sort(want)
		if got := (Evidence{Base: &b}).Keys(); !slices.Equal(got, want) {
			t.Fatalf("base run %+v keyed %q, want %q", b, got, want)
		}
	})
}

func TestFingerprintKeepsFailuresThatDifferInAnyField(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := FailureKey{Objective: "O", Lang: "go", Keys: []string{"test:T"}}
		if StopFingerprint([]FailureKey{a, a}, nil) != StopFingerprint([]FailureKey{a}, nil) {
			t.Fatal("an identical failure listed twice changed the fingerprint")
		}
		b := FailureKey{Objective: a.Objective, Lang: a.Lang, Keys: slices.Clone(a.Keys)}
		switch field := rapid.SampledFrom([]string{"objective", "lang", "keys"}).Draw(t, "field"); field {
		case "objective":
			b.Objective = "P"
		case "lang":
			b.Lang = "ts"
		default:
			b.Keys = []string{"test:U"}
		}
		if StopFingerprint([]FailureKey{a, b}, nil) == StopFingerprint([]FailureKey{a}, nil) {
			t.Fatalf("a second failure differing only in %v was merged into the first, so they would share a block budget", b)
		}
	})
}
