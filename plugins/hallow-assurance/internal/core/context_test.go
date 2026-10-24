package core

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func contextSections(out string) map[string][]string {
	sections := map[string][]string{}
	header := ""
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "Objectives "):
			header = l
			sections[header] = []string{}
		case strings.HasPrefix(l, "    ") && header != "":
			f := strings.Fields(l)
			sections[header] = append(sections[header], f[0]+" "+f[1])
		case l == "":
		default:
			header = ""
		}
	}
	return sections
}

func rowFor(rows []string, id string) string {
	for _, r := range rows {
		if strings.HasPrefix(r, id+" ") {
			return r
		}
	}
	return ""
}

func inputsManifest(t *rapid.T, c Catalog) (*Manifest, bool) {
	levelGen := rapid.SampledFrom([]Level{"A", "B", "C", "D"})
	m := &Manifest{DefaultLevel: levelGen.Draw(t, "default"), Catalog: c}
	anyInputs := false
	for i := range rapid.IntRange(0, 4).Draw(t, "ncomp") {
		g, err := CompileGlob(fmt.Sprintf("c%d/**", i))
		if err != nil {
			t.Fatal(err)
		}
		comp := Component{Glob: g, Level: levelGen.Draw(t, "level"), Inputs: rapid.Bool().Draw(t, "inputs")}
		anyInputs = anyInputs || comp.Inputs
		m.Components = append(m.Components, comp)
	}
	return m, anyInputs
}

func checkInputsComponent(t *rapid.T, fuzz Objective, comp Component, out string, sections map[string][]string) {
	lineTagged := strings.Contains(out, fmt.Sprintf("  %s  level %s [inputs]\n", comp.Glob, comp.Level))
	header := fmt.Sprintf("Objectives for %s (inputs, level %s):", comp.Glob, comp.Level)
	rows, hasBlock := sections[header]
	if lineTagged != comp.Inputs || hasBlock != comp.Inputs {
		t.Fatalf("component %s inputs=%v: line tagged inputs=%v, inputs block present=%v; the agent learns a component handles untrusted input only from its tag and block\n%s", comp.Glob, comp.Inputs, lineTagged, hasBlock, out)
	}
	if !comp.Inputs {
		return
	}
	want := ""
	if s := fuzz.Levels[comp.Level]; s != "" {
		want = fuzz.ID + " " + s
	}
	if got := rowFor(rows, fuzz.ID); got != want {
		t.Fatalf("under %q the %s row = %q, want %q; an inputs component must show fuzzing with the catalog's status at its level\n%s", header, fuzz.ID, got, want, out)
	}
}

func TestContextListsInputsObjectivesOnlyUnderInputsComponents(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	var fuzz Objective
	for _, o := range c.Objectives {
		if o.ID == "VER-ROBUST-FUZZ" {
			fuzz = o
		}
	}
	if fuzz.AppliesTo != "inputs" || fuzz.Levels["B"] != "required" {
		t.Fatalf("catalog VER-ROBUST-FUZZ = %+v, want applies_to inputs and required at B; the spec scenarios are stated for that row", fuzz)
	}
	rapid.Check(t, func(t *rapid.T) {
		m, anyInputs := inputsManifest(t, c)
		out := RenderContext(m, nil)
		if !anyInputs && strings.Contains(out, fuzz.ID) {
			t.Fatalf("no component declares inputs: true, yet %s appears; context must list only what evaluate can decide for this manifest\n%s", fuzz.ID, out)
		}
		sections := contextSections(out)
		for header, rows := range sections {
			if strings.HasPrefix(header, "Objectives at level ") && rowFor(rows, fuzz.ID) != "" {
				t.Fatalf("%s listed under %q; the per-level list would claim fuzzing applies to every path at that level, not only input-handling components\n%s", fuzz.ID, header, out)
			}
		}
		for _, comp := range m.Components {
			checkInputsComponent(t, fuzz, comp, out, sections)
		}
	})
}

func checkContextSelection(t *rapid.T, m *Manifest, only []string, wantDefault bool, picked map[int]bool) {
	out := RenderContext(m, only)
	if got := strings.Contains(out, "\nDefault level: "+string(m.DefaultLevel)+" "); got != wantDefault {
		t.Fatalf("paths %q: default-level line shown=%v, want %v; the default level concerns the reader only when some path matches no component\n%s", only, got, wantDefault, out)
	}
	if got := strings.Contains(out, "\nComponents ("); got != (len(picked) > 0) {
		t.Fatalf("paths %q: components header shown=%v with %d components selected\n%s", only, got, len(picked), out)
	}
	for i, comp := range m.Components {
		suffix := ""
		if comp.Inputs {
			suffix = " [inputs]"
		}
		line := fmt.Sprintf("\n  %s  level %s%s\n", comp.Glob, comp.Level, suffix)
		if got := strings.Contains(out, line); got != picked[i] {
			t.Fatalf("paths %q: line %q shown=%v, want %v; context lists exactly the components the paths resolve to, tagged only with what they declare\n%s", only, line, got, picked[i], out)
		}
	}
}

func TestContextForPathsShowsOnlyWhatTheyResolveTo(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		m, _ := inputsManifest(t, c)
		var only []string
		unmatched, picked := false, map[int]bool{}
		for range rapid.IntRange(1, 3).Draw(t, "npaths") {
			i := rapid.IntRange(-1, len(m.Components)-1).Draw(t, "component")
			if i < 0 {
				only, unmatched = append(only, "other/x.go"), true
				continue
			}
			only, picked[i] = append(only, fmt.Sprintf("c%d/x.go", i)), true
		}
		checkContextSelection(t, m, only, unmatched, picked)
		all := map[int]bool{}
		for i := range m.Components {
			all[i] = true
		}
		checkContextSelection(t, m, nil, true, all)
	})
}

func TestContextListsNoProtectedFilesOrRemovedObjectives(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		m, _ := inputsManifest(t, c)
		out := RenderContext(m, nil)
		for _, banned := range []string{"Protected", "IND-", "CFG-"} {
			if strings.Contains(out, banned) {
				t.Fatalf("context contains %q; nothing restricts edits and those objectives are gone, so it must not tell the agent otherwise\n%s", banned, out)
			}
		}
	})
}
