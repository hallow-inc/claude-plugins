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
	Failing  []string
}

type xmlCase struct {
	Name      string    `xml:"name,attr"`
	Classname string    `xml:"classname,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

type xmlSuite struct {
	Suites []xmlSuite `xml:"testsuite"`
	Cases  []xmlCase  `xml:"testcase"`
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
		j.add(root)
		return j, nil
	}
}

func (j *JUnit) add(s xmlSuite) {
	for _, c := range s.Cases {
		j.Cases++
		name := c.Name
		if c.Classname != "" {
			name = c.Classname + "." + c.Name
		}
		switch {
		case c.Failure != nil:
			j.Failures++
			j.Failing = append(j.Failing, name)
		case c.Error != nil:
			j.Errors++
			j.Failing = append(j.Failing, name)
		case c.Skipped != nil:
			j.Skipped++
		}
	}
	for _, sub := range s.Suites {
		j.add(sub)
	}
}
