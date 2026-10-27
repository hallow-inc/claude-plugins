package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

func changedTestFiles(c runContext, unit string) ([]string, error) {
	paths, err := adapterkit.ChangedPaths(c.root, c.ref)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		role, claimed, err := classify(p)
		if err != nil {
			return nil, err
		}
		if !claimed || role != "test" {
			continue
		}
		if u, ok := unitOf(c.top, filepath.Dir(filepath.Join(c.root, filepath.FromSlash(p)))); ok && u == unit {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

type selectedTest struct {
	file, id string
}

func isTestModule(p string) bool {
	return filepath.Base(p) != "conftest.py"
}

// selectChanged lists the tests of the current test files whose identity is new or whose text
// (decorators included) differs from the base tree's copy of the same file.
func selectChanged(python, root, baseRoot string, files []string) ([]selectedTest, []adapterkit.XMLCase, error) {
	var head, base []string
	for _, f := range files {
		if !isTestModule(f) || !adapterkit.Exists(filepath.Join(root, filepath.FromSlash(f))) {
			continue
		}
		head = append(head, filepath.Join(root, filepath.FromSlash(f)))
		if p := filepath.Join(baseRoot, filepath.FromSlash(f)); adapterkit.Exists(p) {
			base = append(base, p)
		}
	}
	headInfo, err := describeFiles(python, root, head)
	if err != nil {
		return nil, nil, err
	}
	baseInfo, err := describeFiles(python, baseRoot, base)
	if err != nil {
		return nil, nil, err
	}
	var selected []selectedTest
	var unparsed []adapterkit.XMLCase
	for _, f := range files {
		h, ok := headInfo[filepath.Join(root, filepath.FromSlash(f))]
		if !ok {
			continue
		}
		if h.Error != "" {
			unparsed = append(unparsed, adapterkit.XMLCase{Name: collectionCase, Classname: f, Error: &adapterkit.XMLText{Message: "test file does not parse", Body: h.Error}})
			continue
		}
		old := map[string]string{}
		for _, t := range baseInfo[filepath.Join(baseRoot, filepath.FromSlash(f))].Tests {
			old[t.id(f)] = t.Text
		}
		for _, t := range h.Tests {
			if text, ok := old[t.id(f)]; !ok || text != t.Text {
				selected = append(selected, selectedTest{file: f, id: t.id(f)})
			}
		}
	}
	return selected, unparsed, nil
}

func baseName(caseName string) string {
	if i := strings.Index(caseName, "["); i >= 0 {
		return caseName[:i]
	}
	return caseName
}

// foldCase gives one result for a test from all its parameter instances: a failure if any failed,
// else an error if any errored, else skipped if all skipped, else passed.
func foldCase(name, file string, instances []adapterkit.XMLCase) adapterkit.XMLCase {
	return foldInstances(name, file, instances, "fails on the base commit", "errors on the base commit")
}

func foldInstances(name, file string, instances []adapterkit.XMLCase, failMsg, errMsg string) adapterkit.XMLCase {
	tc := adapterkit.XMLCase{Name: name, Classname: file}
	var failures, errs []string
	skipped := 0
	for _, in := range instances {
		switch {
		case in.Failure != nil:
			failures = append(failures, in.Name+": "+in.Failure.Body)
		case in.Error != nil:
			errs = append(errs, in.Name+": "+in.Error.Body)
		case in.Skipped != nil:
			skipped++
		}
	}
	switch {
	case len(failures) > 0:
		tc.Failure = &adapterkit.XMLText{Message: failMsg, Body: adapterkit.CapText([]byte(strings.Join(failures, "\n\n")))}
	case len(errs) > 0:
		tc.Error = &adapterkit.XMLText{Message: errMsg, Body: adapterkit.CapText([]byte(strings.Join(errs, "\n\n")))}
	case skipped == len(instances):
		tc.Skipped = &struct{}{}
	}
	return tc
}

func baseCases(selected []selectedTest, suites []adapterkit.XMLSuite, output []byte) []adapterkit.XMLSuite {
	instances := map[string][]adapterkit.XMLCase{}
	collection := map[string]*adapterkit.XMLText{}
	for _, s := range suites {
		for _, c := range s.Cases {
			if c.Name == collectionCase {
				collection[c.Classname] = c.Error
				continue
			}
			key := c.Classname + "::" + baseName(c.Name)
			instances[key] = append(instances[key], c)
		}
	}
	byFile := map[string]*adapterkit.XMLSuite{}
	for _, t := range selected {
		s, ok := byFile[t.file]
		if !ok {
			s = &adapterkit.XMLSuite{Name: t.file, Cases: []adapterkit.XMLCase{}}
			byFile[t.file] = s
		}
		name := strings.TrimPrefix(t.id, t.file+"::")
		switch in := instances[t.id]; {
		case collection[t.file] != nil:
			s.Cases = append(s.Cases, adapterkit.XMLCase{Name: name, Classname: t.file, Error: &adapterkit.XMLText{Message: "could not be collected on the base commit", Body: collection[t.file].Body}})
		case len(in) == 0:
			s.Cases = append(s.Cases, adapterkit.XMLCase{Name: name, Classname: t.file, Error: &adapterkit.XMLText{Message: "did not run on the base commit", Body: adapterkit.CapText(output)}})
		default:
			s.Cases = append(s.Cases, foldCase(name, t.file, in))
		}
	}
	out := []adapterkit.XMLSuite{}
	for _, f := range slices.Sorted(maps.Keys(byFile)) {
		s := byFile[f]
		s.Tests = len(s.Cases)
		out = append(out, *s)
	}
	return out
}

func unitFailOnBase(c runContext, unit string) ([]adapterkit.XMLSuite, string, error) {
	u := c.units[unit]
	if msg, err := discoveryRefusal(u.python, c.top, unit); err != nil || msg != "" {
		return nil, msg, err
	}
	files, err := changedTestFiles(c, unit)
	if err != nil || len(files) == 0 {
		return nil, "", err
	}
	tmp, err := os.MkdirTemp("", "assure-fail-on-base-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	dst := realPath(tmp)
	tree := filepath.Join(dst, "tree")
	if err := baseTree(c.top, c.ref, unit, tree); err != nil {
		return nil, "", err
	}
	baseRoot := filepath.Join(tree, filepath.FromSlash(adapterkit.RelSlash(c.top, c.root)))
	selected, unparsed, err := selectChanged(u.python, c.root, baseRoot, files)
	if err != nil {
		return nil, "", err
	}
	if err := overlayTests(c.root, baseRoot, files); err != nil {
		return nil, "", err
	}
	var suites []adapterkit.XMLSuite
	for _, tc := range unparsed {
		suites = append(suites, adapterkit.XMLSuite{Name: tc.Classname, Tests: 1, Cases: []adapterkit.XMLCase{tc}})
	}
	if len(selected) == 0 {
		return suites, "", nil
	}
	got, msg, err := runSelected(c, unit, dst, baseRoot, selected)
	if err != nil || msg != "" {
		return nil, msg, err
	}
	return append(suites, got...), "", nil
}

func runSelected(c runContext, unit, dst, baseRoot string, selected []selectedTest) ([]adapterkit.XMLSuite, string, error) {
	tree := filepath.Join(dst, "tree")
	baseUnit := filepath.Join(tree, filepath.FromSlash(adapterkit.RelSlash(c.top, unit)))
	if msg, err := probeTree(c.units[unit].python, c.top, tree, baseUnit); err != nil || msg != "" {
		return nil, msg, err
	}
	argsFile := filepath.Join(dst, "selected.args")
	var files []string
	for _, t := range selected {
		files = append(files, adapterkit.RelSlash(baseUnit, filepath.Join(baseRoot, filepath.FromSlash(t.file))))
	}
	slices.Sort(files)
	if err := os.WriteFile(argsFile, []byte(strings.Join(slices.Compact(files), "\n")+"\n"), 0o644); err != nil {
		return nil, "", err
	}
	got, output, err := c.pytest(baseUnit, unit, filepath.Join(dst, "fail-on-base.xml"), baseRoot, tree, treeEnv(baseUnit), "@"+argsFile)
	if err != nil {
		return nil, "", err
	}
	return baseCases(selected, got, output), "", nil
}

func runFailOnBase(c runContext) ([]evidence, string, error) {
	suites := []adapterkit.XMLSuite{}
	for _, unit := range slices.Sorted(maps.Keys(c.sel)) {
		if len(c.sel[unit]) == 0 {
			continue
		}
		s, msg, err := unitFailOnBase(c, unit)
		if err != nil || msg != "" {
			return nil, msg, err
		}
		suites = append(suites, s...)
	}
	if err := writeJUnit(filepath.Join(c.out, "fail-on-base.xml"), suites); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "test.fail_on_base", Path: "fail-on-base.xml"}}, "", nil
}
