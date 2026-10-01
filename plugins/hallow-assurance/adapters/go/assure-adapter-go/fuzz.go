package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type xmlProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func changedSources(root, ref string) ([]string, error) {
	paths, err := changedPaths(root, ref)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		role, err := goRole(root, p)
		if err != nil {
			return nil, err
		}
		if role != "source" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func goRole(root, p string) (string, error) {
	role, _ := globRole(p)
	if !strings.HasSuffix(p, ".go") || ignoredByGo(p) {
		return "", nil
	}
	gen, err := hasGeneratedHeader(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "", err
	}
	if gen {
		return "generated", nil
	}
	return role, nil
}

func fuzzTargets(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		fns, err := testFuncs(src)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for n := range fns {
			if strings.HasPrefix(n, "Fuzz") {
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func runFuzz(root, ref, out string, _ map[string][]string, _ map[string]string) ([]evidence, error) {
	sources, err := changedSources(root, ref)
	if err != nil {
		return nil, err
	}
	byDir := map[string][]string{}
	var dirs []string
	for _, p := range sources {
		d := path.Dir(p)
		if byDir[d] == nil {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], p)
	}
	sort.Strings(dirs)
	seed, err := rapidSeed(root)
	if err != nil {
		return nil, err
	}
	suites := xmlFuzzSuites{Suites: []xmlFuzzSuite{}}
	for _, dir := range dirs {
		mod, ok := moduleOf(root, dir)
		if !ok {
			continue
		}
		s, err := fuzzSuite(root, mod, dir, seed)
		if err != nil {
			return nil, err
		}
		for _, p := range byDir[dir] {
			s.Properties = append(s.Properties, xmlProperty{Name: "assure.source", Value: p})
		}
		suites.Suites = append(suites.Suites, s)
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "fuzz.xml"), append([]byte(xml.Header), data...), 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "fuzz.run", Path: "fuzz.xml"}}, nil
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

func fuzzSuite(root, mod, dir, seed string) (xmlFuzzSuite, error) {
	names, err := fuzzTargets(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return xmlFuzzSuite{}, err
	}
	s := xmlFuzzSuite{Name: dir, Cases: []xmlCase{}}
	if len(names) == 0 {
		return s, nil
	}
	rel := "./" + path.Clean(strings.TrimPrefix(strings.TrimPrefix(dir, mod), "/"))
	cmd := exec.CommandContext(context.Background(), "go", "test", "-json", "-run", "^Fuzz", rel)
	cmd.Dir = filepath.Join(root, mod)
	cmd.Env = append(os.Environ(), "RAPID_SEED="+seed)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			return xmlFuzzSuite{}, err
		}
	}
	s.Cases = fuzzCases(dir, names, parseEvents(stdout.Bytes()), stderr.String())
	s.Tests = len(s.Cases)
	return s, nil
}

func foldFuzzEvents(events []event) (result map[string]string, outs map[string]*strings.Builder, pkgOut *strings.Builder) {
	result, outs, pkgOut = map[string]string{}, map[string]*strings.Builder{}, &strings.Builder{}
	for _, e := range events {
		top, _, _ := strings.Cut(e.Test, "/")
		switch {
		case e.Test == "":
			if e.Action == "output" || strings.HasPrefix(e.Action, "build-") {
				appendCapped(pkgOut, e.Output)
			}
		case e.Action == "output":
			if outs[top] == nil {
				outs[top] = &strings.Builder{}
			}
			appendCapped(outs[top], e.Output)
		case e.Test == top && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip"):
			result[top] = e.Action
		}
	}
	return result, outs, pkgOut
}

func fuzzCases(dir string, names []string, events []event, stderr string) []xmlCase {
	result, outs, pkgOut := foldFuzzEvents(events)
	appendCapped(pkgOut, stderr)
	cases := []xmlCase{}
	for _, n := range names {
		c := xmlCase{Name: n, Classname: dir}
		body := ""
		if b := outs[n]; b != nil {
			body = b.String()
		}
		switch result[n] {
		case "pass":
		case "fail":
			c.Failure = &xmlText{Message: "fuzz target failed on its seed corpus", Body: body}
		case "skip":
			c.Skipped = &struct{}{}
		default:
			c.Error = &xmlText{Message: "fuzz target did not run", Body: capText([]byte(pkgOut.String()))}
		}
		cases = append(cases, c)
	}
	return cases
}
