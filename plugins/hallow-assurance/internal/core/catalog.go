package core

import (
	"encoding/json"
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
	Evidence  string
	Levels    map[Level]string
	Threshold map[Level]float64
	Budget    map[Level]Budget
	AppliesTo string
}

type Budget struct {
	Floor        int
	LinesPerCase int
}

const budgetEvidence = "test.budget"

const ProvenanceEvidence = "provenance.chain"

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
		o := Objective{ID: m["id"].(string), Title: m["title"].(string), Evidence: m["evidence"].(string), Levels: map[Level]string{}}
		if seen[o.ID] {
			p.add(ptr+"/id", "unique", "duplicate objective id %s", o.ID)
		}
		seen[o.ID] = true
		for l, s := range m["levels"].(map[string]any) {
			o.Levels[Level(l)] = s.(string)
		}
		for _, field := range []string{"threshold", "budget"} {
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
		if th, ok := m["threshold"].(map[string]any); ok {
			o.Threshold = map[Level]float64{}
			for l, v := range th {
				f, _ := v.(json.Number).Float64()
				o.Threshold[Level(l)] = f
			}
		}
		o.Budget = budgetOf(p, ptr, o, m)
		o.AppliesTo, _ = m["applies_to"].(string)
		c.Objectives = append(c.Objectives, o)
	}
	return c, p.err(file)
}

func budgetOf(p *problems, ptr string, o Objective, m map[string]any) map[Level]Budget {
	perLevel, has := m["budget"].(map[string]any)
	if o.Evidence != budgetEvidence {
		if has {
			p.add(ptr+"/budget", "budget", "budget is only allowed on %s objectives", budgetEvidence)
		}
		return nil
	}
	if _, ok := m["threshold"]; ok {
		p.add(ptr+"/threshold", "budget", "%s objectives take a budget, not a threshold", budgetEvidence)
	}
	out := map[Level]Budget{}
	for _, l := range levels {
		if _, applies := o.Levels[l]; !applies {
			continue
		}
		b, ok := perLevel[string(l)].(map[string]any)
		if !ok {
			p.add(ptr+"/budget/"+string(l), "budget", "%s has no budget for level %s", o.ID, l)
			continue
		}
		floor, _ := b["floor"].(json.Number).Int64()
		lpc, _ := b["lines_per_case"].(json.Number).Int64()
		out[l] = Budget{Floor: int(floor), LinesPerCase: int(lpc)}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
