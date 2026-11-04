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

func FuzzFindings(o Objective, changed []ChangedFile, run FuzzRun) []Finding {
	return fuzzFindings(o, changed, run)
}

func fuzzFindings(o Objective, changed []ChangedFile, run FuzzRun) []Finding {
	var out []Finding
	for _, f := range changed {
		if f.Role != Source || !inScope(o, f) || o.Levels[f.Level] == "" {
			continue
		}
		at := func(text string) {
			out = append(out, Finding{Path: f.Path, Level: f.Level, Located: true, Text: text})
		}
		cases, named := fileCases(run, f.Path)
		if !named {
			at(fmt.Sprintf("%s: no fuzz evidence names this file", f.Path))
			continue
		}
		if len(cases) == 0 {
			at(fmt.Sprintf("%s: no fuzz target covers this file", f.Path))
		}
		for _, c := range cases {
			if c.Outcome != fuzzPassed {
				at(fmt.Sprintf("%s: fuzz target %s %s on its seed corpus", f.Path, c.Name, c.Outcome))
			}
		}
	}
	slices.SortFunc(out, compareFindings)
	return out
}

func fileCases(run FuzzRun, path string) (cases []FuzzCase, named bool) {
	for _, s := range run.Suites {
		if slices.Contains(s.Sources, path) {
			named = true
			cases = append(cases, s.Cases...)
		}
	}
	return cases, named
}
