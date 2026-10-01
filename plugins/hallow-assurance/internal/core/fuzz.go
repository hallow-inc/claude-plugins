package core

import (
	"fmt"
	"slices"
)

type FuzzRun struct {
	Suites []FuzzSuite
}

type FuzzSuite struct {
	Sources []string
	Cases   []FuzzCase
}

type FuzzCase struct {
	Name    string
	Outcome string
}

const fuzzPassed = "passed"

func fuzzFindings(o Objective, changed []ChangedFile, run FuzzRun) []Finding {
	var out []Finding
	for _, f := range changed {
		if f.Role != Source || !inScope(o, f) || o.Levels[f.Level] == "" {
			continue
		}
		at := func(text string) {
			out = append(out, Finding{Path: f.Path, Level: f.Level, Located: true, Text: text})
		}
		i := slices.IndexFunc(run.Suites, func(s FuzzSuite) bool { return slices.Contains(s.Sources, f.Path) })
		if i < 0 {
			at(fmt.Sprintf("%s: no fuzz evidence names this file", f.Path))
			continue
		}
		s := run.Suites[i]
		if len(s.Cases) == 0 {
			at(fmt.Sprintf("%s: no fuzz target covers this file", f.Path))
		}
		for _, c := range s.Cases {
			if c.Outcome != fuzzPassed {
				at(fmt.Sprintf("%s: fuzz target %s %s on its seed corpus", f.Path, c.Name, c.Outcome))
			}
		}
	}
	slices.SortFunc(out, compareFindings)
	return out
}
