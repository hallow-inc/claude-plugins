package core

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var (
	tieSegGen  = rapid.SampledFrom([]string{"a", "b"})
	tiePathGen = rapid.Custom(func(t *rapid.T) string {
		return strings.Join(rapid.SliceOfN(tieSegGen, 1, 3).Draw(t, "segs"), "/")
	})
	tieComponentGen = rapid.Custom(func(t *rapid.T) Component {
		segs := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b", "*", "**"}), 1, 3).Draw(t, "segs")
		g, err := CompileGlob(strings.Join(segs, "/"))
		if err != nil {
			t.Fatal(err)
		}
		return Component{
			Glob:   g,
			Level:  rapid.SampledFrom(levels).Draw(t, "level"),
			Formal: rapid.Bool().Draw(t, "formal"),
			Inputs: rapid.Bool().Draw(t, "inputs"),
		}
	})
)

func tiedWinners(comps []Component, p string) (Level, []Component) {
	best := -1
	for _, c := range comps {
		if c.Glob.Match(p) {
			best = max(best, c.Glob.Literals())
		}
	}
	var top []Component
	for _, c := range comps {
		if c.Glob.Match(p) && c.Glob.Literals() == best {
			top = append(top, c)
		}
	}
	if len(top) == 0 {
		return "", nil
	}
	lvl := slices.MinFunc(top, func(a, b Component) int { return strings.Compare(string(a.Level), string(b.Level)) }).Level
	return lvl, slices.DeleteFunc(top, func(c Component) bool { return c.Level != lvl })
}

func canonicalContext(out string) string {
	blocks := strings.Split(strings.TrimSuffix(out, "\n"), "\n\n")
	for i, b := range blocks {
		lines := strings.Split(b, "\n")
		slices.Sort(lines)
		blocks[i] = strings.Join(lines, "\n")
	}
	slices.Sort(blocks)
	return strings.Join(blocks, "\n\n")
}

func checkTieWinners(t *rapid.T, comps []Component, p string, got ChangedFile, out string) {
	lvl, tied := tiedWinners(comps, p)
	if len(tied) == 0 {
		return
	}
	formal := slices.ContainsFunc(tied, func(c Component) bool { return c.Formal })
	inputs := slices.ContainsFunc(tied, func(c Component) bool { return c.Inputs })
	if got.Level != lvl || got.Formal != formal || got.Inputs != inputs {
		t.Fatalf("%q: ChangedFile = level %s formal=%v inputs=%v, want level %s formal=%v inputs=%v (union over tied components %v); a flag dropped from a tie switches off the formal or VER-ROBUST-FUZZ objectives a tied component asked for", p, got.Level, got.Formal, got.Inputs, lvl, formal, inputs, tied)
	}
	for _, c := range tied {
		if !strings.Contains(out, fmt.Sprintf("\n  %s  level %s", c.Glob, c.Level)) {
			t.Fatalf("context for %q omits tied component %s; the agent would not see the objectives that component applies to this path\n%s", p, c.Glob, out)
		}
	}
}

func TestEqualLevelTieIsIndependentOfComponentOrder(t *testing.T) {
	cat, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		comps := rapid.SliceOfN(tieComponentGen, 2, 8).Draw(t, "comps")
		p := tiePathGen.Draw(t, "path")
		reversed := slices.Clone(comps)
		slices.Reverse(reversed)
		m := &Manifest{Root: "/repo", DefaultLevel: "C", Catalog: cat, Components: comps}
		for _, other := range [][]Component{reversed, rapid.Permutation(comps).Draw(t, "perm")} {
			checkSameResolution(t, p, m, &Manifest{Root: "/repo", DefaultLevel: "C", Catalog: cat, Components: other})
		}
		checkTieWinners(t, comps, p, m.ChangedFile(p), RenderContext(m, []string{p}))
	})
}

func checkSameResolution(t *rapid.T, p string, m, pm *Manifest) {
	want, got := m.ChangedFile(p), pm.ChangedFile(p)
	if want.Level != got.Level || want.Formal != got.Formal || want.Inputs != got.Inputs {
		t.Fatalf("%q: listed order gives level %s formal=%v inputs=%v, permuted order gives level %s formal=%v inputs=%v; formal objectives and VER-ROBUST-FUZZ would apply or not depending on manifest order", p, want.Level, want.Formal, want.Inputs, got.Level, got.Formal, got.Inputs)
	}
	out, pout := RenderContext(m, []string{p}), RenderContext(pm, []string{p})
	if canonicalContext(out) != canonicalContext(pout) {
		t.Fatalf("context for %q depends on component order; the agent would be told different objectives for the same manifest\nlisted:\n%s\npermuted:\n%s", p, out, pout)
	}
}

func TestEqualLevelTieUnionsFlags(t *testing.T) {
	inputs := Component{Glob: mustGlob(t, "internal/**"), Level: "C", Inputs: true}
	formal := Component{Glob: mustGlob(t, "**/billing/**"), Level: "C", Formal: true}
	const p = "internal/billing/x.go"
	for _, comps := range [][]Component{{inputs, formal}, {formal, inputs}} {
		m := manifestWith("D", comps...)
		order := []string{comps[0].Glob.String(), comps[1].Glob.String()}
		if f := m.ChangedFile(p); f.Level != "C" || !f.Formal || !f.Inputs {
			t.Errorf("order %v: %s = level %s formal=%v inputs=%v, want level C formal and inputs; dropping either flag switches off the formal objectives or VER-ROBUST-FUZZ for this path depending on manifest order", order, p, f.Level, f.Formal, f.Inputs)
		}
		out := RenderContext(m, []string{p})
		for _, line := range []string{"  internal/**  level C [inputs]\n", "  **/billing/**  level C [formal]\n"} {
			if !strings.Contains(out, line) {
				t.Errorf("order %v: context for %s lacks %q; the agent would not see that component's objectives\n%s", order, p, line, out)
			}
		}
	}
}
