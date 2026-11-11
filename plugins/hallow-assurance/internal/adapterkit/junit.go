package adapterkit

import (
	"encoding/xml"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type XMLText struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type XMLCase struct {
	Name       string        `xml:"name,attr"`
	Classname  string        `xml:"classname,attr"`
	Properties []XMLProperty `xml:"properties>property"`
	Failure    *XMLText      `xml:"failure"`
	Error      *XMLText      `xml:"error"`
	Skipped    *struct{}     `xml:"skipped"`
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

const RerunRounds = 3

type rerunKey struct{ classname, name string }

func Rerun(cases []XMLCase, rerun func(failing []XMLCase) ([]XMLCase, error)) []XMLCase {
	out := slices.Clone(cases)
	settled := make([]bool, len(out))
	for i, c := range out {
		settled[i] = c.Failure == nil || c.Error != nil
	}
	for round := 1; round <= RerunRounds; round++ {
		var idx []int
		var failing []XMLCase
		for i, c := range out {
			if !settled[i] {
				idx = append(idx, i)
				failing = append(failing, c)
			}
		}
		if len(failing) == 0 {
			break
		}
		results, err := rerun(failing)
		if err != nil {
			continue
		}
		seen := map[rerunKey]int{}
		passed := map[rerunKey]bool{}
		for _, r := range results {
			k := rerunKey{r.Classname, r.Name}
			seen[k]++
			passed[k] = r.Failure == nil && r.Error == nil && r.Skipped == nil
		}
		for _, i := range idx {
			k := rerunKey{out[i].Classname, out[i].Name}
			if seen[k] == 1 && passed[k] {
				settled[i] = true
				out[i].Properties = append(slices.Clip(out[i].Properties), XMLProperty{Name: "assure.flaky", Value: strconv.Itoa(round)})
			}
		}
	}
	return out
}
