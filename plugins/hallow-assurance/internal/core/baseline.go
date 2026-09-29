package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const BaselineFile = ".assure/baseline.json"

type BaselineEntry struct {
	Objective string `json:"objective"`
	Rule      string `json:"rule"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	Count     int    `json:"count"`
}

type Fingerprint struct {
	Objective, Rule, Path, Message string
}

func (e BaselineEntry) Fingerprint() Fingerprint {
	return Fingerprint{e.Objective, e.Rule, e.Path, e.Message}
}

type Baseline struct {
	Entries []BaselineEntry
}

type baselineDoc struct {
	Version int             `json:"version"`
	Entries []BaselineEntry `json:"entries"`
}

func LoadBaseline(root string) (Baseline, error) {
	data, err := os.ReadFile(filepath.Join(root, BaselineFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Baseline{}, nil
	}
	if err != nil {
		return Baseline{}, err
	}
	return ParseBaseline(BaselineFile, data)
}

func ParseBaseline(file string, data []byte) (Baseline, error) {
	doc, err := validated(schemas.Baseline, file, data)
	if err != nil {
		return Baseline{}, err
	}
	var bd baselineDoc
	if err := json.Unmarshal(data, &bd); err != nil {
		return Baseline{}, fmt.Errorf("%s: %w", file, err)
	}
	p := &problems{doc: doc}
	first := map[Fingerprint]int{}
	for i, e := range bd.Entries {
		if j, dup := first[e.Fingerprint()]; dup {
			p.add(fmt.Sprintf("/entries/%d", i), "unique", "entry %d repeats entry %d (%s %s %s)", i, j, e.Objective, e.Rule, e.Path)
			continue
		}
		first[e.Fingerprint()] = i
	}
	return Baseline{Entries: bd.Entries}, p.err(file)
}

func (b Baseline) For(objective string) map[Fingerprint]int {
	out := map[Fingerprint]int{}
	for _, e := range b.Entries {
		if e.Objective == objective {
			out[e.Fingerprint()] = e.Count
		}
	}
	return out
}

func NewBaseline(fps []Fingerprint) Baseline {
	counts := map[Fingerprint]int{}
	for _, fp := range fps {
		counts[fp]++
	}
	var b Baseline
	for fp, n := range counts {
		b.Entries = append(b.Entries, BaselineEntry{fp.Objective, fp.Rule, fp.Path, fp.Message, n})
	}
	sort.Slice(b.Entries, func(i, j int) bool {
		x, y := b.Entries[i], b.Entries[j]
		if x.Objective != y.Objective {
			return x.Objective < y.Objective
		}
		if x.Path != y.Path {
			return x.Path < y.Path
		}
		if x.Rule != y.Rule {
			return x.Rule < y.Rule
		}
		return x.Message < y.Message
	})
	return b
}

func (b Baseline) Marshal() []byte {
	entries := b.Entries
	if entries == nil {
		entries = []BaselineEntry{}
	}
	data, err := json.MarshalIndent(baselineDoc{Version: 0, Entries: entries}, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func matchBaseline(allowed map[Fingerprint]int, objective string, findings []Finding) ([]Finding, map[Fingerprint]int) {
	used := map[Fingerprint]int{}
	var kept []Finding
	for _, f := range findings {
		fp := Fingerprint{objective, f.Rule, f.Path, f.Message}
		if f.Located && used[fp] < allowed[fp] {
			used[fp]++
			continue
		}
		kept = append(kept, f)
	}
	return kept, used
}

type Removable struct {
	Entry  BaselineEntry
	Unused int
}

func (b Baseline) Removable(used map[Fingerprint]int, considered func(BaselineEntry) bool) []Removable {
	var out []Removable
	for _, e := range b.Entries {
		if n := e.Count - used[e.Fingerprint()]; n > 0 && considered(e) {
			out = append(out, Removable{Entry: e, Unused: n})
		}
	}
	return out
}
