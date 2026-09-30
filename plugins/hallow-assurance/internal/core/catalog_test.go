package core

import (
	"encoding/json"
	"errors"
	"fmt"
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
		}
		if absent != "" {
			cs = append(cs,
				corruption{"threshold for absent level", fmt.Sprintf("/%d/threshold/%s", i, absent), func(o map[string]any) { o["threshold"].(map[string]any)[absent] = 1 }},
				corruption{"independence for absent level", fmt.Sprintf("/%d/independence/%s", i, absent), func(o map[string]any) { o["independence"] = map[string]any{absent: "tier1"} }})
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
}

func TestUnknownCatalogVersion(t *testing.T) {
	if _, err := LoadCatalog("v9"); err == nil {
		t.Fatal("v9 loaded")
	}
}

func TestStarterCatalogKeepsComplexityThreshold(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range c.Objectives {
		if o.ID == "CODE-COMPLEXITY" {
			if got := o.Threshold["B"]; got != 15 {
				t.Fatalf("CODE-COMPLEXITY threshold at B = %v, want 15; without it every function counts as a finding", got)
			}
			return
		}
	}
	t.Fatal("CODE-COMPLEXITY missing from catalog v0")
}
