package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

type vitestAssertion struct {
	AncestorTitles  []string      `json:"ancestorTitles"`
	Title           string        `json:"title"`
	Status          string        `json:"status"`
	FailureMessages []string      `json:"failureMessages"`
	Location        *taskLocation `json:"location"`
}

type vitestFile struct {
	Name             string            `json:"name"`
	Status           string            `json:"status"`
	Message          string            `json:"message"`
	AssertionResults []vitestAssertion `json:"assertionResults"`
}

type vitestReport struct {
	TestResults []vitestFile `json:"testResults"`
}

var skippedStatuses = []string{"skipped", "pending", "todo", "disabled"}

func relTo(root, p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func vitestCase(file string, a vitestAssertion) (adapterkit.XMLCase, bool) {
	tc := adapterkit.XMLCase{Name: strings.Join(append(slices.Clone(a.AncestorTitles), a.Title), " > "), Classname: file}
	if a.Status == "passed" {
		return tc, false
	}
	if slices.Contains(skippedStatuses, a.Status) {
		tc.Skipped = &struct{}{}
		return tc, false
	}
	if a.Status == "failed" {
		tc.Failure = &adapterkit.XMLText{Message: "test failed", Body: adapterkit.CapText([]byte(strings.Join(a.FailureMessages, "\n")))}
		return tc, true
	}
	tc.Error = &adapterkit.XMLText{Message: "unknown vitest status " + a.Status, Body: "no output"}
	return tc, true
}

func toJUnit(root string, r vitestReport) []adapterkit.XMLSuite {
	out := []adapterkit.XMLSuite{}
	for _, f := range r.TestResults {
		file := relTo(root, f.Name)
		s := adapterkit.XMLSuite{Name: file, Cases: []adapterkit.XMLCase{}}
		failed := false
		for _, a := range f.AssertionResults {
			tc, bad := vitestCase(file, a)
			failed = failed || bad
			s.Cases = append(s.Cases, tc)
		}
		if f.Status == "failed" && !failed {
			s.Cases = append(s.Cases, adapterkit.XMLCase{Name: "(file)", Classname: file, Error: &adapterkit.XMLText{Message: "test file failed without a failing test", Body: adapterkit.CapText([]byte(f.Message))}})
		}
		s.Tests = len(s.Cases)
		out = append(out, s)
	}
	return out
}

func errorSuite(name, msg string) adapterkit.XMLSuite {
	return adapterkit.XMLSuite{Name: name, Tests: 1, Cases: []adapterkit.XMLCase{{Name: "(run)", Classname: name, Error: &adapterkit.XMLText{Message: "vitest did not run", Body: msg}}}}
}

func hasFailure(suites []adapterkit.XMLSuite) bool {
	for _, s := range suites {
		for _, c := range s.Cases {
			if c.Failure != nil || c.Error != nil {
				return true
			}
		}
	}
	return false
}

func vitestSuites(root, unit, report string, output []byte, code int) []adapterkit.XMLSuite {
	data, err := os.ReadFile(report)
	var r vitestReport
	if err != nil || json.Unmarshal(data, &r) != nil {
		return []adapterkit.XMLSuite{errorSuite("unit "+unit, adapterkit.CapText(output))}
	}
	suites := toJUnit(root, r)
	if code != 0 && !hasFailure(suites) {
		suites = append(suites, errorSuite("unit "+unit, adapterkit.CapText(output)))
	}
	return suites
}
