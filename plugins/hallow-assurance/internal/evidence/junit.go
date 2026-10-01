package evidence

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

type JUnit struct {
	Cases    int
	Failures int
	Errors   int
	Skipped  int
	Failing  []Failure
	Passed   []string
	Skips    []string
	Suites   []Suite
}

type Suite struct {
	Name       string
	Properties []Property
	Cases      []Case
}

type Property struct {
	Name  string
	Value string
}

type Outcome string

const (
	Passed  Outcome = "passed"
	Failed  Outcome = "failed"
	Errored Outcome = "errored"
	Skipped Outcome = "skipped"
)

type Case struct {
	Name    string
	Outcome Outcome
}

type Failure struct {
	Name    string
	Message string
	Text    string
	Error   bool
}

type xmlOutcome struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type xmlCase struct {
	Name      string      `xml:"name,attr"`
	Classname string      `xml:"classname,attr"`
	Failure   *xmlOutcome `xml:"failure"`
	Error     *xmlOutcome `xml:"error"`
	Skipped   *struct{}   `xml:"skipped"`
}

type xmlProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlSuite struct {
	Name       string        `xml:"name,attr"`
	Properties []xmlProperty `xml:"properties>property"`
	Suites     []xmlSuite    `xml:"testsuite"`
	Cases      []xmlCase     `xml:"testcase"`
}

func ParseJUnit(data []byte) (JUnit, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return JUnit{}, fmt.Errorf("junit: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "testsuites" && start.Name.Local != "testsuite" {
			return JUnit{}, fmt.Errorf("junit: root element is <%s>, want <testsuites> or <testsuite>", start.Name.Local)
		}
		var root xmlSuite
		if err := dec.DecodeElement(&root, &start); err != nil {
			return JUnit{}, fmt.Errorf("junit: %w", err)
		}
		var j JUnit
		j.add(root, start.Name.Local == "testsuite")
		return j, nil
	}
}

func (j *JUnit) add(s xmlSuite, isSuite bool) {
	suite := Suite{Name: s.Name}
	for _, p := range s.Properties {
		suite.Properties = append(suite.Properties, Property(p))
	}
	for _, c := range s.Cases {
		j.Cases++
		name := c.Name
		if c.Classname != "" {
			name = c.Classname + "." + c.Name
		}
		switch {
		case c.Failure != nil:
			suite.Cases = append(suite.Cases, Case{Name: name, Outcome: Failed})
			j.Failures++
			j.Failing = append(j.Failing, Failure{Name: name, Message: c.Failure.Message, Text: c.Failure.Text})
		case c.Error != nil:
			suite.Cases = append(suite.Cases, Case{Name: name, Outcome: Errored})
			j.Errors++
			j.Failing = append(j.Failing, Failure{Name: name, Message: c.Error.Message, Text: c.Error.Text, Error: true})
		case c.Skipped != nil:
			suite.Cases = append(suite.Cases, Case{Name: name, Outcome: Skipped})
			j.Skipped++
			j.Skips = append(j.Skips, name)
		default:
			suite.Cases = append(suite.Cases, Case{Name: name, Outcome: Passed})
			j.Passed = append(j.Passed, name)
		}
	}
	if isSuite {
		j.Suites = append(j.Suites, suite)
	}
	for _, sub := range s.Suites {
		j.add(sub, true)
	}
}
