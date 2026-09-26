package core

import (
	"fmt"
	"sort"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/catalog"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type Level string

var levels = []Level{"A", "B", "C", "D"}

func stricter(a, b Level) bool { return a < b }

type Objective struct {
	ID        string
	Title     string
	Levels    map[Level]string
	AppliesTo string
}

type Catalog struct {
	Version    string
	Objectives []Objective
}

func LoadCatalog(version string) (Catalog, error) {
	data, ok := catalog.Version(version)
	if !ok {
		return Catalog{}, fmt.Errorf("catalog %q is not embedded in this assure binary", version)
	}
	c, err := parseCatalog("catalog/"+version+"/objectives.yaml", data)
	c.Version = version
	return c, err
}

func parseCatalog(file string, data []byte) (Catalog, error) {
	doc, err := validated(schemas.Catalog, file, data)
	if err != nil {
		return Catalog{}, err
	}
	p := &problems{doc: doc}
	items := doc.Value.([]any)
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.(map[string]any)["id"].(string)] = false
	}
	var c Catalog
	for i, it := range items {
		m := it.(map[string]any)
		ptr := fmt.Sprintf("/%d", i)
		o := Objective{ID: m["id"].(string), Title: m["title"].(string), Levels: map[Level]string{}}
		if seen[o.ID] {
			p.add(ptr+"/id", "unique", "duplicate objective id %s", o.ID)
		}
		seen[o.ID] = true
		for l, s := range m["levels"].(map[string]any) {
			o.Levels[Level(l)] = s.(string)
		}
		for _, field := range []string{"threshold", "independence"} {
			perLevel, _ := m[field].(map[string]any)
			for _, l := range sortedKeys(perLevel) {
				if _, ok := o.Levels[Level(l)]; !ok {
					p.add(ptr+"/"+field+"/"+l, "levels", "%s names level %s, which %s does not apply at", field, l, o.ID)
				}
			}
		}
		if alt, ok := m["alternative_for"].(string); ok {
			if _, exists := seen[alt]; !exists || alt == o.ID {
				p.add(ptr+"/alternative_for", "reference", "alternative_for %s names no other objective in this catalog", alt)
			}
		}
		o.AppliesTo, _ = m["applies_to"].(string)
		c.Objectives = append(c.Objectives, o)
	}
	return c, p.err(file)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
