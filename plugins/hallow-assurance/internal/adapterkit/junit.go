package adapterkit

import (
	"encoding/xml"
	"strings"
	"unicode/utf8"
)

type XMLText struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type XMLCase struct {
	Name      string    `xml:"name,attr"`
	Classname string    `xml:"classname,attr"`
	Failure   *XMLText  `xml:"failure"`
	Error     *XMLText  `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

type XMLSuite struct {
	Name  string    `xml:"name,attr"`
	Tests int       `xml:"tests,attr"`
	Cases []XMLCase `xml:"testcase"`
}

type XMLSuites struct {
	XMLName xml.Name   `xml:"testsuites"`
	Suites  []XMLSuite `xml:"testsuite"`
}

type XMLProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type XMLFuzzSuite struct {
	Name       string        `xml:"name,attr"`
	Tests      int           `xml:"tests,attr"`
	Properties []XMLProperty `xml:"properties>property"`
	Cases      []XMLCase     `xml:"testcase"`
}

type XMLFuzzSuites struct {
	XMLName xml.Name       `xml:"testsuites"`
	Suites  []XMLFuzzSuite `xml:"testsuite"`
}

const MaxOutput = 16 << 10

func CapText(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > MaxOutput {
		n := MaxOutput
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
