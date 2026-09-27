package evidence

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type genCase struct {
	name, class, kind string
}

type genSuite struct {
	cases  []genCase
	suites []genSuite
}

var kinds = []string{"pass", "failure", "error", "skipped"}

func suiteGen(depth int) *rapid.Generator[genSuite] {
	return rapid.Custom(func(t *rapid.T) genSuite {
		var s genSuite
		for range rapid.IntRange(0, 4).Draw(t, "ncases") {
			s.cases = append(s.cases, genCase{
				name:  rapid.StringMatching(`[A-Za-z][A-Za-z0-9_/]{0,8}`).Draw(t, "name"),
				class: rapid.SampledFrom([]string{"", "pkg", "example.com/m/x"}).Draw(t, "class"),
				kind:  rapid.SampledFrom(kinds).Draw(t, "kind"),
			})
		}
		if depth > 0 {
			for range rapid.IntRange(0, 2).Draw(t, "nsuites") {
				s.suites = append(s.suites, suiteGen(depth-1).Draw(t, "suite"))
			}
		}
		return s
	})
}

func (s genSuite) xml(b *strings.Builder, t *rapid.T, tag string) {
	fmt.Fprintf(b, `<%s tests="%d" failures="%d" errors="%d">`, tag,
		rapid.IntRange(0, 9).Draw(t, "attrTests"), rapid.IntRange(0, 9).Draw(t, "attrFail"), rapid.IntRange(0, 9).Draw(t, "attrErr"))
	for _, c := range s.cases {
		fmt.Fprintf(b, `<testcase name="%s" classname="%s">`, c.name, c.class)
		switch c.kind {
		case "failure":
			b.WriteString(`<failure message="boom">trace</failure>`)
		case "error":
			b.WriteString(`<error message="boom"/>`)
		case "skipped":
			b.WriteString(`<skipped/>`)
		}
		b.WriteString(`</testcase>`)
	}
	for _, sub := range s.suites {
		sub.xml(b, t, "testsuite")
	}
	fmt.Fprintf(b, `</%s>`, tag)
}

func (s genSuite) want() JUnit {
	var j JUnit
	for _, c := range s.cases {
		j.Cases++
		name := c.name
		if c.class != "" {
			name = c.class + "." + c.name
		}
		switch c.kind {
		case "failure":
			j.Failures++
			j.Failing = append(j.Failing, name)
		case "error":
			j.Errors++
			j.Failing = append(j.Failing, name)
		case "skipped":
			j.Skipped++
		}
	}
	for _, sub := range s.suites {
		w := sub.want()
		j.Cases += w.Cases
		j.Failures += w.Failures
		j.Errors += w.Errors
		j.Skipped += w.Skipped
		j.Failing = append(j.Failing, w.Failing...)
	}
	return j
}

func genDoc(t *rapid.T) (string, JUnit) {
	s := suiteGen(2).Draw(t, "root")
	var b strings.Builder
	if rapid.Bool().Draw(t, "decl") {
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	}
	s.xml(&b, t, rapid.SampledFrom([]string{"testsuites", "testsuite"}).Draw(t, "rootTag"))
	return b.String(), s.want()
}

func TestJUnitCountsComeFromTestcases(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc, want := genDoc(t)
		got, err := ParseJUnit([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		if got.Cases != want.Cases || got.Failures != want.Failures || got.Errors != want.Errors ||
			got.Skipped != want.Skipped || !slices.Equal(got.Failing, want.Failing) {
			t.Fatalf("got %+v, want %+v for %s", got, want, doc)
		}
	})
}

func TestJUnitStrictPrefixIsAnError(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc, _ := genDoc(t)
		n := rapid.IntRange(0, len(doc)-1).Draw(t, "cut")
		if _, err := ParseJUnit([]byte(doc[:n])); err == nil {
			t.Fatalf("prefix %q parsed without error", doc[:n])
		}
	})
}

func TestJUnitAttributeCountsAreNotTrusted(t *testing.T) {
	doc := `<testsuite failures="0"><testcase name="TestX" classname="p"><failure/></testcase></testsuite>`
	got, err := ParseJUnit([]byte(doc))
	if err != nil || got.Failures != 1 || !slices.Equal(got.Failing, []string{"p.TestX"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestJUnitUnknownRoot(t *testing.T) {
	if _, err := ParseJUnit([]byte(`<results/>`)); err == nil || !strings.Contains(err.Error(), "<results>") {
		t.Fatalf("want root error, got %v", err)
	}
}
