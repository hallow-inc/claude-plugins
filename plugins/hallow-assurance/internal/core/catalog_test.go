package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func catalogDoc(t *rapid.T) []map[string]any {
	ids := rapid.SliceOfNDistinct(rapid.StringMatching(`[A-Z]{2,4}-[A-Z0-9]{1,4}`), 1, 5, func(s string) string { return s }).Draw(t, "ids")
	objs := make([]map[string]any, len(ids))
	for i, id := range ids {
		lv := map[string]any{}
		th := map[string]any{}
		for _, l := range levels {
			if rapid.Bool().Draw(t, "has"+string(l)) {
				lv[string(l)] = "required"
				if rapid.Bool().Draw(t, "th"+string(l)) {
					th[string(l)] = 50
				}
			}
		}
		if len(lv) == 0 {
			lv["C"] = "advisory"
		}
		o := map[string]any{"id": id, "title": "t", "source": []any{"s"}, "levels": lv, "evidence": "e", "threshold": th}
		if i > 0 && rapid.Bool().Draw(t, "alt") {
			o["alternative_for"] = ids[rapid.IntRange(0, i-1).Draw(t, "altIdx")]
		}
		objs[i] = o
	}
	return objs
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func problemAt(err error, loc string) bool {
	var le *LoadError
	if !errors.As(err, &le) {
		return false
	}
	for _, p := range le.Problems {
		if p.Location == loc {
			return true
		}
	}
	return false
}

func TestEveryCrossFieldRuleIsEnforcedAtItsLocation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		objs := catalogDoc(t)
		if _, err := parseCatalog("c.yaml", mustJSON(objs)); err != nil {
			t.Fatalf("valid catalog rejected: %v", err)
		}
		i := rapid.IntRange(0, len(objs)-1).Draw(t, "victim")
		lv := objs[i]["levels"].(map[string]any)
		var absent string
		for _, l := range levels {
			if _, ok := lv[string(l)]; !ok {
				absent = string(l)
			}
		}
		type corruption struct {
			name string
			loc  string
			edit func(o map[string]any)
		}
		cs := []corruption{
			{"self alternative", fmt.Sprintf("/%d/alternative_for", i), func(o map[string]any) { o["alternative_for"] = o["id"] }},
			{"dangling alternative", fmt.Sprintf("/%d/alternative_for", i), func(o map[string]any) { o["alternative_for"] = "ZZZZZ-NOPE" }},
			{"removed budget key", fmt.Sprintf("/%d", i), func(o map[string]any) { o["budget"] = map[string]any{"B": map[string]any{"floor": 3}} }},
		}
		if absent != "" {
			cs = append(cs,
				corruption{"threshold for absent level", fmt.Sprintf("/%d/threshold/%s", i, absent), func(o map[string]any) { o["threshold"].(map[string]any)[absent] = 1 }})
		}
		if len(objs) > 1 {
			j := (i + 1) % len(objs)
			loc := fmt.Sprintf("/%d/id", max(i, j))
			cs = append(cs, corruption{"duplicate id", loc, func(o map[string]any) { o["id"] = objs[j]["id"] }})
		}
		c := cs[rapid.IntRange(0, len(cs)-1).Draw(t, "corruption")]
		var bad []map[string]any
		if err := json.Unmarshal(mustJSON(objs), &bad); err != nil {
			t.Fatal(err)
		}
		c.edit(bad[i])
		_, err := parseCatalog("c.yaml", mustJSON(bad))
		if !problemAt(err, c.loc) {
			t.Fatalf("%s: want problem at %s, got %v", c.name, c.loc, err)
		}
	})
}

func TestEmbeddedStarterCatalogLoads(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Objectives) == 0 {
		t.Fatal("v0 has no objectives")
	}
	for _, o := range c.Objectives {
		if o.ID == "VER-TRACE-REQ" || o.ID == "VER-TEST-BUDGET" || strings.HasPrefix(o.ID, "VER-DST-") {
			t.Errorf("v0 contains %s, whose evidence no adapter produces, so it can only ever fail closed", o.ID)
		}
	}
}

func TestUnknownCatalogVersion(t *testing.T) {
	if _, err := LoadCatalog("v9"); err == nil {
		t.Fatal("v9 loaded")
	}
}

func TestStarterCatalogKeepsMutationThreshold(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range c.Objectives {
		if o.ID == "VER-MUTATION-CHANGED" {
			if got := o.Threshold["B"]; got != 65 {
				t.Fatalf("VER-MUTATION-CHANGED threshold at B = %v, want 65; without it a single surviving mutant fails the level", got)
			}
			return
		}
	}
	t.Fatal("VER-MUTATION-CHANGED missing from catalog v0")
}

func TestStarterCatalogHasNoCodeShapeObjectives(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range c.Objectives {
		if strings.HasPrefix(o.ID, "CODE-") || o.Evidence == "lint.sarif" {
			t.Errorf("v0 contains %s (evidence %s); hallow-assurance judges behaviour evidence, and a repo's own lint owns code shape", o.ID, o.Evidence)
		}
	}
}

func TestStarterCatalogRestrictsFailOnBaseToFixes(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range c.Objectives {
		if o.ID == "VER-FAIL-ON-BASE" {
			if o.AppliesTo != "fix" {
				t.Fatalf("VER-FAIL-ON-BASE applies_to = %q, want fix; without it every feature change fails on base by not compiling", o.AppliesTo)
			}
			return
		}
	}
	t.Fatal("VER-FAIL-ON-BASE missing from catalog v0")
}

func TestStarterCatalogFuzzScopeAndNoVerdictTitles(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range c.Objectives {
		if strings.Contains(strings.ToLower(o.Title), "verdict") {
			t.Errorf("%s title %q says verdict; only the evaluator decides, so no objective may claim to", o.ID, o.Title)
		}
		if o.ID == "VER-ROBUST-FUZZ" {
			found = true
			if o.AppliesTo != "inputs" {
				t.Errorf("VER-ROBUST-FUZZ applies_to = %q, want inputs; otherwise it demands fuzz targets for code that takes no external input", o.AppliesTo)
			}
		}
	}
	if !found {
		t.Error("VER-ROBUST-FUZZ missing from catalog v0")
	}
}
