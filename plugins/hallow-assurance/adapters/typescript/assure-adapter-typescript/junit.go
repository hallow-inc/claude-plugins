package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type xmlText struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type xmlCase struct {
	Name      string    `xml:"name,attr"`
	Classname string    `xml:"classname,attr"`
	Failure   *xmlText  `xml:"failure"`
	Error     *xmlText  `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

type xmlSuite struct {
	Name  string    `xml:"name,attr"`
	Tests int       `xml:"tests,attr"`
	Cases []xmlCase `xml:"testcase"`
}

type xmlSuites struct {
	XMLName xml.Name   `xml:"testsuites"`
	Suites  []xmlSuite `xml:"testsuite"`
}

const maxOutput = 16 << 10

func capText(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxOutput {
		s = s[:maxOutput]
	}
	if s == "" {
		s = "no output"
	}
	return s
}

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

func vitestCase(file string, a vitestAssertion) (xmlCase, bool) {
	tc := xmlCase{Name: strings.Join(append(slices.Clone(a.AncestorTitles), a.Title), " > "), Classname: file}
	if a.Status == "passed" {
		return tc, false
	}
	if slices.Contains(skippedStatuses, a.Status) {
		tc.Skipped = &struct{}{}
		return tc, false
	}
	if a.Status == "failed" {
		tc.Failure = &xmlText{Message: "test failed", Body: capText([]byte(strings.Join(a.FailureMessages, "\n")))}
		return tc, true
	}
	tc.Error = &xmlText{Message: "unknown vitest status " + a.Status, Body: "no output"}
	return tc, true
}

func toJUnit(root string, r vitestReport) []xmlSuite {
	out := []xmlSuite{}
	for _, f := range r.TestResults {
		file := relTo(root, f.Name)
		s := xmlSuite{Name: file, Cases: []xmlCase{}}
		failed := false
		for _, a := range f.AssertionResults {
			tc, bad := vitestCase(file, a)
			failed = failed || bad
			s.Cases = append(s.Cases, tc)
		}
		if f.Status == "failed" && !failed {
			s.Cases = append(s.Cases, xmlCase{Name: "(file)", Classname: file, Error: &xmlText{Message: "test file failed without a failing test", Body: capText([]byte(f.Message))}})
		}
		s.Tests = len(s.Cases)
		out = append(out, s)
	}
	return out
}

func errorSuite(name, msg string) xmlSuite {
	return xmlSuite{Name: name, Tests: 1, Cases: []xmlCase{{Name: "(run)", Classname: name, Error: &xmlText{Message: "vitest did not run", Body: msg}}}}
}

func hasFailure(suites []xmlSuite) bool {
	for _, s := range suites {
		for _, c := range s.Cases {
			if c.Failure != nil || c.Error != nil {
				return true
			}
		}
	}
	return false
}

func vitestSuites(root, unit, report string, output []byte, code int) []xmlSuite {
	data, err := os.ReadFile(report)
	var r vitestReport
	if err != nil || json.Unmarshal(data, &r) != nil {
		return []xmlSuite{errorSuite("unit "+unit, capText(output))}
	}
	suites := toJUnit(root, r)
	if code != 0 && !hasFailure(suites) {
		suites = append(suites, errorSuite("unit "+unit, capText(output)))
	}
	return suites
}
