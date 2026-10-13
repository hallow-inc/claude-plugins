package core

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func manifestWith(dflt Level, comps ...Component) *Manifest {
	return &Manifest{Root: "/repo", DefaultLevel: dflt, Components: comps}
}

var componentGen = rapid.Custom(func(t *rapid.T) Component {
	g, err := CompileGlob(globGen.Draw(t, "glob"))
	if err != nil {
		t.Fatal(err)
	}
	return Component{Glob: g, Level: rapid.SampledFrom(levels).Draw(t, "level")}
})

func TestLevelIsIndependentOfComponentOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		comps := rapid.SliceOfN(componentGen, 0, 5).Draw(t, "comps")
		p := pathGen.Draw(t, "path")
		_, want := manifestWith("C", comps...).Resolve(p)
		shuffled := rapid.Permutation(comps).Draw(t, "perm")
		if _, got := manifestWith("C", shuffled...).Resolve(p); got != want {
			t.Fatalf("order changed level for %q: %s vs %s", p, want, got)
		}
	})
}

func TestMoreSpecificComponentDecidesLevel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := pathGen.Draw(t, "path")
		g := mustGlob(t, p)
		var comps []Component
		for _, c := range rapid.SliceOfN(componentGen, 0, 5).Draw(t, "comps") {
			if !c.Glob.Match(p) || c.Glob.Literals() < g.Literals() {
				comps = append(comps, c)
			}
		}
		lvl := rapid.SampledFrom(levels).Draw(t, "lvl")
		exact := Component{Glob: g, Level: lvl}
		if _, got := manifestWith("D", append(comps, exact)...).Resolve(p); got != lvl {
			t.Fatalf("literal component %q at %s lost to a less specific one: got %s", p, lvl, got)
		}
	})
}

func TestSoleMatchingComponentDecidesWhateverItsSpecificity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := pathGen.Draw(t, "path")
		segs := strings.Split(p, "/")
		var globs []string
		for _, g := range []string{"**", strings.Repeat("*/", len(segs)-1) + "*", segs[0] + "/**", "**/" + segs[len(segs)-1], p} {
			if mustGlob(t, g).Match(p) {
				globs = append(globs, g)
			}
		}
		g := rapid.SampledFrom(globs).Draw(t, "glob")
		lvl, dflt := rapid.SampledFrom(levels).Draw(t, "lvl"), rapid.SampledFrom(levels).Draw(t, "dflt")
		if comp, got := manifestWith(dflt, Component{Glob: mustGlob(t, g), Level: lvl}).Resolve(p); comp != 0 || got != lvl {
			t.Fatalf("only %q (level %s) matches %q, default %s: got component %d level %s; a matching component decides however few literal segments it has", g, lvl, p, dflt, comp, got)
		}
	})
}

func TestUnmatchedPathTakesDefault(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := pathGen.Draw(t, "path")
		dflt := rapid.SampledFrom(levels).Draw(t, "dflt")
		comp, got := manifestWith(dflt, Component{Glob: mustGlob(t, "zzz/**"), Level: "A"}).Resolve(p)
		if strings.HasPrefix(p, "zzz") {
			t.Skip()
		}
		if comp != -1 || got != dflt {
			t.Fatalf("unmatched %q: component %d level %s, want -1 %s", p, comp, got, dflt)
		}
	})
}

func TestLevelScenarios(t *testing.T) {
	c := func(g string, l Level) Component { return Component{Glob: mustGlob(t, g), Level: l} }
	cases := []struct {
		comps []Component
		path  string
		want  Level
	}{
		{[]Component{c("internal/**", "C"), c("internal/billing/**", "A")}, "internal/billing/post.go", "A"},
		{[]Component{c("internal/**", "C"), c("**/billing/**", "B")}, "internal/billing/post.go", "B"},
		{[]Component{c("**/billing/**", "B"), c("internal/**", "C")}, "internal/billing/post.go", "B"},
		{nil, "tools/gen.go", "C"},
	}
	for _, tc := range cases {
		if _, got := manifestWith("C", tc.comps...).Resolve(tc.path); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestRel(t *testing.T) {
	m := manifestWith("C")
	if rel, ok := m.Rel("/repo/internal/chat/a.go"); !ok || rel != "internal/chat/a.go" {
		t.Errorf("inside: %q %v", rel, ok)
	}
	for _, p := range []string{"/other/a.go", "/repo/../other/a.go", "/"} {
		if rel, ok := m.Rel(p); ok {
			t.Errorf("%s resolved inside as %q", p, rel)
		}
	}
	if rel, ok := m.Rel("/repo"); !ok || rel != "." {
		t.Errorf("root itself: %q %v", rel, ok)
	}
}
