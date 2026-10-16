package main

import (
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type xmlProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlFuzzSuite struct {
	Name       string        `xml:"name,attr"`
	Tests      int           `xml:"tests,attr"`
	Properties []xmlProperty `xml:"properties>property"`
	Cases      []xmlCase     `xml:"testcase"`
}

type xmlFuzzSuites struct {
	XMLName xml.Name       `xml:"testsuites"`
	Suites  []xmlFuzzSuite `xml:"testsuite"`
}

func fuzzFiles(root, src string) []string {
	var out []string
	for _, e := range sourceExts {
		stem, ok := strings.CutSuffix(src, "."+e)
		if !ok {
			continue
		}
		for _, x := range sourceExts {
			f := stem + ".fuzz.test." + x
			if exists(filepath.Join(root, filepath.FromSlash(f))) {
				out = append(out, f)
			}
		}
	}
	return out
}

func changedSources(c runContext, unit string) ([]string, error) {
	var out []string
	for _, f := range c.sel[unit] {
		role, _, err := classify(f)
		if err != nil {
			return nil, err
		}
		if role == "source" {
			out = append(out, f)
		}
	}
	return out, nil
}

func unitFuzz(c runContext, unit, seed, tmp string, i int) ([]xmlFuzzSuite, error) {
	sources, err := changedSources(c, unit)
	if err != nil {
		return nil, err
	}
	targets := map[string][]string{}
	var all []string
	for _, src := range sources {
		targets[src] = fuzzFiles(c.root, src)
		all = append(all, targets[src]...)
	}
	byFile := map[string][]xmlCase{}
	var runErrors []xmlCase
	if len(all) > 0 {
		slices.Sort(all)
		report := filepath.Join(tmp, fmt.Sprintf("fuzz-%d.json", i))
		args := append([]string{"run", "--reporter=json", "--outputFile=" + report}, absFiles(c.root, slices.Compact(all))...)
		stdout, stderr, code, err := runToolEnv(unit, []string{"ASSURE_SEED=" + seed}, c.tools[unit]["vitest"].bin, args...)
		if err != nil {
			return nil, err
		}
		name := relSlash(c.top, unit)
		for _, s := range vitestSuites(c.root, name, report, append(stdout, stderr...), code) {
			if s.Name == "unit "+name {
				runErrors = append(runErrors, s.Cases...)
				continue
			}
			byFile[s.Name] = append(byFile[s.Name], s.Cases...)
		}
	}
	out := make([]xmlFuzzSuite, 0, len(sources))
	for _, src := range sources {
		s := xmlFuzzSuite{Name: src, Properties: []xmlProperty{{Name: "assure.source", Value: src}}, Cases: []xmlCase{}}
		for _, f := range targets[src] {
			s.Cases = append(s.Cases, byFile[f]...)
		}
		if len(targets[src]) > 0 {
			s.Cases = append(s.Cases, runErrors...)
		}
		s.Tests = len(s.Cases)
		out = append(out, s)
	}
	return out, nil
}

func runFuzz(c runContext) ([]evidence, string, error) {
	seed, err := assureSeed(c.root)
	if err != nil {
		return nil, "", err
	}
	tmp, err := os.MkdirTemp("", "assure-fuzz-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	suites := xmlFuzzSuites{Suites: []xmlFuzzSuite{}}
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		s, err := unitFuzz(c, unit, seed, tmp, i)
		if err != nil {
			return nil, "", err
		}
		suites.Suites = append(suites.Suites, s...)
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(c.out, "fuzz.xml"), append([]byte(xml.Header), data...), 0o644); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "fuzz.run", Path: "fuzz.xml"}}, "", nil
}
