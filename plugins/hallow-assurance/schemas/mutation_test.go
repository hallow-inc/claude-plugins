package schemas

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

var constraintKeywords = []string{
	"type", "enum", "const", "pattern", "required", "additionalProperties", "minItems", "minLength",
	"minimum", "maximum", "minProperties", "format", "not", "uniqueItems",
}

var equivalentMutants = map[string]string{
	"provenance.schema.json#/type":            "the JSONL decoder already rejects any line that is not an object",
	"common.schema.json#/$defs/repoPath/type": "not:{pattern} also rejects non-strings, because the inner pattern passes them vacuously",
}

type site struct {
	file    string
	path    []string
	keyword string
}

func (s site) String() string {
	return s.file + "#/" + strings.Join(append(slices.Clone(s.path), s.keyword), "/")
}

func constraintSites(file string, node any, path []string) []site {
	obj, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	var out []site
	for _, kw := range constraintKeywords {
		if _, ok := obj[kw]; ok {
			out = append(out, site{file, slices.Clone(path), kw})
		}
	}
	for _, kw := range []string{"properties", "patternProperties", "$defs"} {
		if m, ok := obj[kw].(map[string]any); ok {
			for name, sub := range m {
				out = append(out, constraintSites(file, sub, append(slices.Clone(path), kw, name))...)
			}
		}
	}
	for _, kw := range []string{"items", "additionalProperties", "not"} {
		out = append(out, constraintSites(file, obj[kw], append(slices.Clone(path), kw))...)
	}
	return out
}

func mutate(src []byte, s site) []byte {
	var doc any
	if err := json.Unmarshal(src, &doc); err != nil {
		panic(err)
	}
	node := doc.(map[string]any)
	for _, tok := range s.path {
		node = node[tok].(map[string]any)
	}
	delete(node, s.keyword)
	out, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return out
}

func killed(v validator) bool {
	if len(checkFixtures(os.DirFS("testdata"), v)) > 0 {
		return true
	}
	for k, gen := range generators {
		for seed := range 8 {
			if len(checkSample(v, k, gen.Example(seed))) > 0 {
				return true
			}
		}
	}
	return false
}

func TestEveryConstraintIsLoadBearing(t *testing.T) {
	if testing.Short() {
		t.Skip("schema mutation run is slow")
	}
	srcs, err := embedded()
	if err != nil {
		t.Fatal(err)
	}
	var sites []site
	for file, data := range srcs {
		if strings.HasPrefix(file, "external/") {
			continue
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		sites = append(sites, constraintSites(file, doc, nil)...)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].String() < sites[j].String() })
	if len(sites) < 50 {
		t.Fatalf("found only %d constraint sites; the walker is missing schema structure", len(sites))
	}
	for _, s := range sites {
		mutant := make(map[string][]byte, len(srcs))
		for f, d := range srcs {
			mutant[f] = d
		}
		mutant[s.file] = mutate(srcs[s.file], s)
		v, err := compile(mutant)
		if err != nil {
			t.Errorf("mutant %s does not compile: %v", s, err)
			continue
		}
		survived := !killed(v)
		reason, equivalent := equivalentMutants[s.String()]
		switch {
		case survived && !equivalent:
			t.Errorf("deleting %s changes no test outcome: the constraint is untested", s)
		case !survived && equivalent:
			t.Errorf("%s is listed as equivalent (%s) but a test now catches its removal; drop the entry", s, reason)
		}
	}
}
