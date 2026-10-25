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

// fuzzFiles gives a source file's fuzz files that exist: test_<stem>_fuzz.py beside it, and the
// same name under the unit's tests/ mirroring its directory with a leading src/ removed.
func fuzzFiles(root, unit, src string) []string {
	abs := filepath.Join(root, filepath.FromSlash(src))
	dir, err := filepath.Rel(unit, filepath.Dir(abs))
	if err != nil {
		return nil
	}
	dir = filepath.ToSlash(dir)
	name := "test_" + strings.TrimSuffix(filepath.Base(abs), ".py") + "_fuzz.py"
	mirror := dir
	if dir == "src" {
		mirror = "."
	} else if rest, ok := strings.CutPrefix(dir, "src/"); ok {
		mirror = rest
	}
	var out []string
	for _, p := range []string{filepath.Join(filepath.Dir(abs), name), filepath.Join(unit, "tests", filepath.FromSlash(mirror), name)} {
		if exists(p) {
			out = append(out, relSlash(root, p))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
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

func foldSuite(file string, cases []xmlCase) []xmlCase {
	instances := map[string][]xmlCase{}
	var names []string
	var out []xmlCase
	for _, c := range cases {
		if c.Name == collectionCase {
			out = append(out, c)
			continue
		}
		n := baseName(c.Name)
		if _, ok := instances[n]; !ok {
			names = append(names, n)
		}
		instances[n] = append(instances[n], c)
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, foldInstances(n, file, instances[n], "property failed", "property errored"))
	}
	return out
}

func unitFuzz(c runContext, unit, tmp string, i int) ([]xmlFuzzSuite, string, error) {
	sources, err := changedSources(c, unit)
	if err != nil || len(sources) == 0 {
		return nil, "", err
	}
	if msg, err := discoveryRefusal(c.units[unit].python, c.top, unit); err != nil || msg != "" {
		return nil, msg, err
	}
	targets := map[string][]string{}
	var all []string
	for _, src := range sources {
		targets[src] = fuzzFiles(c.root, unit, src)
		all = append(all, targets[src]...)
	}
	byFile := map[string][]xmlCase{}
	if len(all) > 0 {
		slices.Sort(all)
		args := absFiles(c.root, slices.Compact(all))
		suites, _, err := c.pytest(unit, unit, filepath.Join(tmp, fmt.Sprintf("fuzz-%d.xml", i)), c.root, c.top, nil, args...)
		if err != nil {
			return nil, "", err
		}
		for _, s := range suites {
			byFile[s.Name] = foldSuite(s.Name, s.Cases)
		}
	}
	out := make([]xmlFuzzSuite, 0, len(sources))
	for _, src := range sources {
		s := xmlFuzzSuite{Name: src, Properties: []xmlProperty{{Name: "assure.source", Value: src}}, Cases: []xmlCase{}}
		for _, f := range targets[src] {
			s.Cases = append(s.Cases, byFile[f]...)
		}
		s.Tests = len(s.Cases)
		out = append(out, s)
	}
	return out, "", nil
}

func absFiles(root string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Join(root, filepath.FromSlash(f)))
	}
	return out
}

func runFuzz(c runContext) ([]evidence, string, error) {
	tmp, err := os.MkdirTemp("", "assure-fuzz-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	suites := xmlFuzzSuites{Suites: []xmlFuzzSuite{}}
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		s, msg, err := unitFuzz(c, unit, tmp, i)
		if err != nil || msg != "" {
			return nil, msg, err
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
