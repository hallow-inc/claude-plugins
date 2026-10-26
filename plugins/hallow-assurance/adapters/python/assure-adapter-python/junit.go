package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
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
		n := maxOutput
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = s[:n]
	}
	if s == "" {
		s = "no output"
	}
	return s
}

type pytestCase struct {
	Classname string    `xml:"classname,attr"`
	Name      string    `xml:"name,attr"`
	File      string    `xml:"file,attr"`
	Failures  []xmlText `xml:"failure"`
	Errors    []xmlText `xml:"error"`
	Skipped   *xmlText  `xml:"skipped"`
}

func joinTexts(texts []xmlText) *xmlText {
	if len(texts) == 0 {
		return nil
	}
	body := texts[0].Body
	for _, t := range texts[1:] {
		body += "\n\n" + t.Message + "\n" + t.Body
	}
	return &xmlText{Message: texts[0].Message, Body: capText([]byte(body))}
}

type pytestReport struct {
	Suites []struct {
		Cases []pytestCase `xml:"testcase"`
	} `xml:"testsuite"`
}

const collectionCase = "(collection)"

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

func pytestFile(top, unit, file string) string {
	for dir := unit; ; dir = filepath.Dir(dir) {
		if p := filepath.Join(dir, filepath.FromSlash(file)); exists(p) {
			return p
		}
		if dir == top || filepath.Dir(dir) == dir {
			return filepath.Join(unit, filepath.FromSlash(file))
		}
	}
}

func caseName(c pytestCase) string {
	module := strings.ReplaceAll(strings.TrimSuffix(c.File, ".py"), "/", ".")
	if class, ok := strings.CutPrefix(c.Classname, module+"."); ok && class != "" {
		return strings.ReplaceAll(class, ".", "::") + "::" + c.Name
	}
	return c.Name
}

func convertCase(file string, c pytestCase) xmlCase {
	errs := joinTexts(c.Errors)
	if c.Classname == "" && errs != nil {
		return xmlCase{Name: collectionCase, Classname: file, Error: &xmlText{Message: "collection failure", Body: errs.Body}}
	}
	tc := xmlCase{Name: caseName(c), Classname: file}
	switch {
	case len(c.Failures) > 0:
		tc.Failure = joinTexts(append(slices.Clone(c.Failures), c.Errors...))
	case errs != nil:
		tc.Error = errs
	case c.Skipped != nil:
		tc.Skipped = &struct{}{}
	}
	return tc
}

// mergeCase folds the second <testcase> pytest writes for a test whose teardown errors after its
// call phase already reported a result, so one test keeps one result.
func mergeCase(a, b xmlCase) xmlCase {
	failure, errs := a.Failure, a.Error
	if failure == nil {
		failure = b.Failure
	}
	if errs == nil {
		errs = b.Error
	}
	if failure != nil && errs != nil {
		failure = &xmlText{Message: failure.Message, Body: capText([]byte(failure.Body + "\n\n" + errs.Message + "\n" + errs.Body))}
		errs = nil
	}
	a.Failure, a.Error = failure, errs
	if a.Failure != nil || a.Error != nil {
		a.Skipped = nil
	}
	return a
}

func toJUnit(root, top, unit string, r pytestReport) []xmlSuite {
	byFile := map[string]*xmlSuite{}
	var order []string
	for _, s := range r.Suites {
		for _, c := range s.Cases {
			file := relTo(root, pytestFile(top, unit, c.File))
			suite, ok := byFile[file]
			if !ok {
				suite = &xmlSuite{Name: file, Cases: []xmlCase{}}
				byFile[file] = suite
				order = append(order, file)
			}
			tc := convertCase(file, c)
			if i := slices.IndexFunc(suite.Cases, func(o xmlCase) bool { return o.Name == tc.Name }); i >= 0 {
				suite.Cases[i] = mergeCase(suite.Cases[i], tc)
				continue
			}
			suite.Cases = append(suite.Cases, tc)
		}
	}
	slices.Sort(order)
	out := []xmlSuite{}
	for _, f := range order {
		s := byFile[f]
		slices.SortStableFunc(s.Cases, func(a, b xmlCase) int { return strings.Compare(a.Name, b.Name) })
		s.Tests = len(s.Cases)
		out = append(out, *s)
	}
	return out
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

func readPytestReport(path string) (pytestReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pytestReport{}, err
	}
	var r pytestReport
	return r, xml.Unmarshal(data, &r)
}

func writeJUnit(path string, suites []xmlSuite) error {
	data, err := xml.MarshalIndent(xmlSuites{Suites: suites}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), data...), 0o644)
}
