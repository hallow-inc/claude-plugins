package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

type pytestCase struct {
	Classname string               `xml:"classname,attr"`
	Name      string               `xml:"name,attr"`
	File      string               `xml:"file,attr"`
	Failures  []adapterkit.XMLText `xml:"failure"`
	Errors    []adapterkit.XMLText `xml:"error"`
	Skipped   *adapterkit.XMLText  `xml:"skipped"`
}

func joinTexts(texts []adapterkit.XMLText) *adapterkit.XMLText {
	if len(texts) == 0 {
		return nil
	}
	body := texts[0].Body
	for _, t := range texts[1:] {
		body += "\n\n" + t.Message + "\n" + t.Body
	}
	return &adapterkit.XMLText{Message: texts[0].Message, Body: adapterkit.CapText([]byte(body))}
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
		if p := filepath.Join(dir, filepath.FromSlash(file)); adapterkit.Exists(p) {
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

func convertCase(file string, c pytestCase) adapterkit.XMLCase {
	errs := joinTexts(c.Errors)
	if c.Classname == "" && errs != nil {
		return adapterkit.XMLCase{Name: collectionCase, Classname: file, Error: &adapterkit.XMLText{Message: "collection failure", Body: errs.Body}}
	}
	tc := adapterkit.XMLCase{Name: caseName(c), Classname: file}
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
func mergeCase(a, b adapterkit.XMLCase) adapterkit.XMLCase {
	failure, errs := a.Failure, a.Error
	if failure == nil {
		failure = b.Failure
	}
	if errs == nil {
		errs = b.Error
	}
	if failure != nil && errs != nil {
		failure = &adapterkit.XMLText{Message: failure.Message, Body: adapterkit.CapText([]byte(failure.Body + "\n\n" + errs.Message + "\n" + errs.Body))}
		errs = nil
	}
	a.Failure, a.Error = failure, errs
	if a.Failure != nil || a.Error != nil {
		a.Skipped = nil
	}
	return a
}

func toJUnit(root, top, unit string, r pytestReport) []adapterkit.XMLSuite {
	byFile := map[string]*adapterkit.XMLSuite{}
	var order []string
	for _, s := range r.Suites {
		for _, c := range s.Cases {
			file := relTo(root, pytestFile(top, unit, c.File))
			suite, ok := byFile[file]
			if !ok {
				suite = &adapterkit.XMLSuite{Name: file, Cases: []adapterkit.XMLCase{}}
				byFile[file] = suite
				order = append(order, file)
			}
			tc := convertCase(file, c)
			if i := slices.IndexFunc(suite.Cases, func(o adapterkit.XMLCase) bool { return o.Name == tc.Name }); i >= 0 {
				suite.Cases[i] = mergeCase(suite.Cases[i], tc)
				continue
			}
			suite.Cases = append(suite.Cases, tc)
		}
	}
	slices.Sort(order)
	out := []adapterkit.XMLSuite{}
	for _, f := range order {
		s := byFile[f]
		slices.SortStableFunc(s.Cases, func(a, b adapterkit.XMLCase) int { return strings.Compare(a.Name, b.Name) })
		s.Tests = len(s.Cases)
		out = append(out, *s)
	}
	return out
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

func readPytestReport(path string) (pytestReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pytestReport{}, err
	}
	var r pytestReport
	return r, xml.Unmarshal(data, &r)
}

func writeJUnit(path string, suites []adapterkit.XMLSuite) error {
	data, err := xml.MarshalIndent(adapterkit.XMLSuites{Suites: suites}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), data...), 0o644)
}
