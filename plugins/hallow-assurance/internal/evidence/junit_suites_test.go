package evidence

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestJUnitSuiteCasesPartitionTheCounts(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc, want := genDoc(t)
		got, err := ParseJUnit([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		n := map[Outcome]int{}
		for _, s := range got.Suites {
			for _, c := range s.Cases {
				n[c.Outcome]++
			}
		}
		exact := !strings.Contains(doc, "<testsuites")
		counts := []struct {
			outcome   Outcome
			got, want int
		}{
			{Failed, n[Failed], want.Failures},
			{Errored, n[Errored], want.Errors},
			{Skipped, n[Skipped], want.Skipped},
			{Passed, n[Passed], len(want.Passed)},
		}
		for _, c := range counts {
			// Cases placed directly under <testsuites> belong to no suite, so only a bound holds there.
			if c.got > c.want || (exact && c.got != c.want) {
				t.Fatalf("%s cases in suites = %d, want %d (exact=%v) for %s", c.outcome, c.got, c.want, exact, doc)
			}
		}
	})
}

func TestJUnitSuitePropertiesAreKeptEvenWithoutCases(t *testing.T) {
	// The evaluator must tell "package has no fuzz target" (suite, zero cases) from
	// "package missing" (no suite); dropping empty suites would turn the first into the second.
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-z][a-z0-9/]{0,10}`).Draw(t, "suite")
		n := rapid.IntRange(1, 4).Draw(t, "nprops")
		var b strings.Builder
		b.WriteString(`<testsuites><testsuite name="` + name + `"><properties>`)
		var want []Property
		for range n {
			p := Property{
				Name:  rapid.StringMatching(`assure\.[a-z]{1,8}`).Draw(t, "pname"),
				Value: rapid.StringMatching(`[a-z][a-z0-9_/.]{0,16}`).Draw(t, "pvalue"),
			}
			want = append(want, p)
			b.WriteString(`<property name="` + p.Name + `" value="` + p.Value + `"/>`)
		}
		b.WriteString(`</properties></testsuite></testsuites>`)
		got, err := ParseJUnit([]byte(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Suites) != 1 || got.Suites[0].Name != name || len(got.Suites[0].Cases) != 0 {
			t.Fatalf("suites = %+v, want one empty suite %q", got.Suites, name)
		}
		if len(got.Suites[0].Properties) != n {
			t.Fatalf("properties = %+v, want %+v", got.Suites[0].Properties, want)
		}
		for i, p := range want {
			if got.Suites[0].Properties[i] != p {
				t.Fatalf("property %d = %+v, want %+v", i, got.Suites[0].Properties[i], p)
			}
		}
	})
}
