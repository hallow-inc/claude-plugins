package evidence

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type noisyDoc struct {
	t       *rapid.T
	b       strings.Builder
	failing int
	mayErr  bool
}

func (d *noisyDoc) noise(topLevel, inCase bool) {
	for range rapid.IntRange(0, 2).Draw(d.t, "nnoise") {
		kind := rapid.SampledFrom([]string{"space", "comment", "pi", "system-out", "unknown", "suite"}).Draw(d.t, "noise")
		switch kind {
		case "space":
			d.b.WriteString(rapid.SampledFrom([]string{" ", "\n", "\t", "\r\n  "}).Draw(d.t, "ws"))
		case "comment":
			d.b.WriteString(`<!--` + rapid.StringMatching(`[ A-Za-z0-9<>&]{0,12}`).Draw(d.t, "comment") + `-->`)
		case "pi":
			d.b.WriteString(`<?assure ` + rapid.StringMatching(`[a-z]{0,6}`).Draw(d.t, "pi") + `?>`)
		case "system-out":
			d.b.WriteString(`<system-out>` + escape(rapid.StringMatching(`[ -~\n]{0,16}`).Draw(d.t, "out")) + `</system-out>`)
		case "unknown":
			d.b.WriteString(`<x-meta k="v"><y/>` + escape(rapid.StringMatching(`[ -~]{0,8}`).Draw(d.t, "text")) + `</x-meta>`)
		case "suite":
			s := suiteGen(1).Draw(d.t, "extra")
			s.xml(&d.b, d.t, "testsuite")
			w := s.want()
			d.failing += w.Failures + w.Errors
		}
		if topLevel && kind != "space" && kind != "comment" && kind != "pi" || inCase && kind == "suite" {
			d.mayErr = true
		}
	}
}

func (d *noisyDoc) suite(s genSuite, tag string) {
	fmt.Fprintf(&d.b, `<%s name="s">`, tag)
	d.noise(false, false)
	for _, c := range s.cases {
		fmt.Fprintf(&d.b, `<testcase name="%s" classname="%s">`, c.name, c.class)
		d.noise(false, true)
		switch c.kind {
		case "failure", "error":
			fmt.Fprintf(&d.b, `<%s message="%s">%s</%s>`, c.kind, escape(c.msg), escape(c.text), c.kind)
			d.noise(false, true)
		case "skipped":
			d.b.WriteString(`<skipped/>`)
			d.noise(false, true)
		}
		d.b.WriteString(`</testcase>`)
		d.noise(false, false)
	}
	for _, sub := range s.suites {
		d.suite(sub, "testsuite")
		d.noise(false, false)
	}
	fmt.Fprintf(&d.b, `</%s>`, tag)
}

func TestJUnitNoiseNeverHidesAFailingCase(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root := suiteGen(2).Draw(t, "root")
		d := &noisyDoc{t: t}
		if rapid.Bool().Draw(t, "decl") {
			d.b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
		}
		d.noise(true, false)
		d.suite(root, rapid.SampledFrom([]string{"testsuites", "testsuite"}).Draw(t, "rootTag"))
		d.noise(true, false)
		w := root.want()
		want := d.failing + w.Failures + w.Errors
		doc := d.b.String()
		got, err := ParseJUnit([]byte(doc))
		if err != nil {
			if !d.mayErr {
				t.Fatalf("noise inside the root made the reader fail: %v\n%s", err, doc)
			}
			return
		}
		if len(got.Failing) != want {
			t.Fatalf("read %d failing cases, document holds %d; a hidden failure passes VER-TESTS-PASS\n%s", len(got.Failing), want, doc)
		}
	})
}
