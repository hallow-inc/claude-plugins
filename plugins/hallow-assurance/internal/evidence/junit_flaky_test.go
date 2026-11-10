package evidence

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func withoutFlaky(fs []Failure) []Failure {
	out := make([]Failure, len(fs))
	for i, f := range fs {
		out[i] = Failure{Name: f.Name, Message: f.Message, Text: f.Text, Error: f.Error}
	}
	return out
}

func TestJUnitFlakyFailureIsStillAFailure(t *testing.T) {
	doc := `<testsuite name="s"><testcase name="TestX" classname="p"><properties><property name="assure.flaky" value="2"/></properties><failure message="boom">trace</failure></testcase></testsuite>`
	got, err := ParseJUnit([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got.Failures != 1 || got.Errors != 0 || len(got.Passed) != 0 || len(got.Failing) != 1 {
		t.Fatalf("got %+v; a flaky failure must still count as a failure, never as a pass", got)
	}
	if !slices.Equal(withoutFlaky(got.Failing), []Failure{{Name: "p.TestX", Message: "boom", Text: "trace"}}) {
		t.Fatalf("got %+v; the flaky failure must keep its name, message and text", got.Failing)
	}
	if r := got.Failing[0].Flaky; r != 2 {
		t.Fatalf("Flaky = %d, want 2: the report must name the rerun that passed", r)
	}
}

func TestJUnitOutOfRangeRerunIsNotFlaky(t *testing.T) {
	for _, v := range []string{"0", "4", "yes", "", " 2", "2 ", "02", "-1", "+1", "1.0", "10"} {
		doc := `<testsuite><testcase name="TestX"><properties><property name="assure.flaky" value="` + v + `"/></properties><failure/></testcase></testsuite>`
		got, err := ParseJUnit([]byte(doc))
		if err != nil {
			t.Fatalf("value %q: %v", v, err)
		}
		if got.Failures != 1 || len(got.Failing) != 1 {
			t.Fatalf("value %q: got %+v, want one failure", v, got)
		}
		if r := got.Failing[0].Flaky; r != 0 {
			t.Errorf("value %q: Flaky = %d, want 0; a malformed adapter value must not downgrade a real failure", v, r)
		}
	}
}

func TestJUnitTwoFlakyPropertiesAreNotFlaky(t *testing.T) {
	doc := `<testsuite><testcase name="TestX"><properties><property name="assure.flaky" value="1"/><property name="assure.flaky" value="1"/></properties><failure/></testcase></testsuite>`
	got, err := ParseJUnit([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got.Failures != 1 || len(got.Failing) != 1 {
		t.Fatalf("got %+v, want one failure", got)
	}
	if got.Failing[0].Flaky != 0 {
		t.Fatalf("Flaky = %d, want 0; an adapter that reports a case twice has not shown which rerun passed", got.Failing[0].Flaky)
	}
}

func TestJUnitErroringCaseIsNeverFlaky(t *testing.T) {
	doc := `<testsuite><testcase name="TestX"><properties><property name="assure.flaky" value="1"/></properties><error message="panic"/></testcase></testsuite>`
	got, err := ParseJUnit([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got.Errors != 1 || got.Failures != 0 || len(got.Failing) != 1 || !got.Failing[0].Error {
		t.Fatalf("got %+v, want one error", got)
	}
	if r := got.Failing[0].Flaky; r != 0 {
		t.Fatalf("Flaky = %d, want 0; an error (build failure, panic) is never a flaky test", r)
	}
}

func TestJUnitCasePropertiesStayOnTheCase(t *testing.T) {
	doc := `<testsuites><testsuite name="s"><testcase name="TestX"><properties><property name="assure.flaky" value="1"/></properties><failure/></testcase></testsuite></testsuites>`
	got, err := ParseJUnit([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Suites) != 1 || len(got.Suites[0].Properties) != 0 {
		t.Fatalf("got suites %+v; a testcase property must not become a suite property such as assure.source", got.Suites)
	}
}

var flakyValues = []string{"1", "2", "3", "0", "4", "yes", "", " 2", "3 ", "02", "-1", "+1", "1.0", "true"}

func caseProps(t *rapid.T, kind string) (string, int) {
	var props strings.Builder
	rerunOf, seen := 0, 0
	for range rapid.IntRange(0, 3).Draw(t, "nprops") {
		name := rapid.SampledFrom([]string{"assure.flaky", "assure.flaky", "flaky", "assure.Flaky", "assure.flakyx", "assure.source"}).Draw(t, "pname")
		value := rapid.StringMatching(`[ -~]{0,4}`).Draw(t, "pvalue")
		if rapid.Bool().Draw(t, "listed") {
			value = rapid.SampledFrom(flakyValues).Draw(t, "pvalue")
		}
		if name == "assure.flaky" {
			seen++
			if value == "1" || value == "2" || value == "3" {
				rerunOf = int(value[0] - '0')
			}
		}
		fmt.Fprintf(&props, `<property name="%s" value="%s"/>`, name, escape(value))
	}
	if kind != "failure" || seen != 1 {
		rerunOf = 0
	}
	if props.Len() == 0 {
		return "", 0
	}
	return `<properties>` + props.String() + `</properties>`, rerunOf
}

func flakyDoc(t *rapid.T, s genSuite, b *strings.Builder, tag string, wantRerun *[]int) {
	fmt.Fprintf(b, `<%s name="s">`, tag)
	for _, c := range s.cases {
		fmt.Fprintf(b, `<testcase name="%s" classname="%s">`, c.name, c.class)
		props, rerunOf := caseProps(t, c.kind)
		before := rapid.Bool().Draw(t, "propsFirst")
		if before {
			b.WriteString(props)
		}
		switch c.kind {
		case "failure", "error":
			fmt.Fprintf(b, `<%s message="%s">%s</%s>`, c.kind, escape(c.msg), escape(c.text), c.kind)
			*wantRerun = append(*wantRerun, rerunOf)
		case "skipped":
			b.WriteString(`<skipped/>`)
		}
		if !before {
			b.WriteString(props)
		}
		b.WriteString(`</testcase>`)
	}
	for _, sub := range s.suites {
		flakyDoc(t, sub, b, "testsuite", wantRerun)
	}
	fmt.Fprintf(b, `</%s>`, tag)
}

func TestJUnitFlakyPropertyNeverChangesTheCounts(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root := suiteGen(2).Draw(t, "root")
		var b strings.Builder
		var wantRerun []int
		flakyDoc(t, root, &b, rapid.SampledFrom([]string{"testsuites", "testsuite"}).Draw(t, "rootTag"), &wantRerun)
		doc := b.String()
		want := root.want()
		got, err := ParseJUnit([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		if got.Cases != want.Cases || got.Failures != want.Failures || got.Errors != want.Errors ||
			got.Skipped != want.Skipped || !slices.Equal(got.Passed, want.Passed) || !slices.Equal(got.Skips, want.Skips) ||
			!slices.Equal(withoutFlaky(got.Failing), want.Failing) {
			t.Fatalf("got %+v, want %+v for %s; an assure.flaky property must never turn a failure into a pass or change a count", got, want, doc)
		}
		for _, s := range got.Suites {
			if len(s.Properties) != 0 {
				t.Fatalf("suite %q has properties %+v for %s; testcase properties leaked into the suite", s.Name, s.Properties, doc)
			}
		}
	})
}

func TestJUnitOnlyAFailureWithRerunOneToThreeIsFlaky(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root := suiteGen(2).Draw(t, "root")
		var b strings.Builder
		var wantRerun []int
		flakyDoc(t, root, &b, rapid.SampledFrom([]string{"testsuites", "testsuite"}).Draw(t, "rootTag"), &wantRerun)
		doc := b.String()
		got, err := ParseJUnit([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		if len(got.Failing) != len(wantRerun) {
			t.Fatalf("got %d failing, want %d for %s", len(got.Failing), len(wantRerun), doc)
		}
		for i, f := range got.Failing {
			if r := f.Flaky; r != wantRerun[i] {
				t.Fatalf("%s (error=%v): Flaky = %d, want %d for %s", f.Name, f.Error, r, wantRerun[i], doc)
			}
		}
	})
}
