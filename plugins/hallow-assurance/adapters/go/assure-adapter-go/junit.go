package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

type event struct {
	Action      string
	Package     string
	Test        string
	Output      string
	ImportPath  string
	FailedBuild string
}

type testState struct {
	action string
	out    strings.Builder
}

type pkgState struct {
	action      string
	failedBuild string
	out         strings.Builder
	order       []string
	tests       map[string]*testState
}

type converter struct {
	order      []string
	pkgs       map[string]*pkgState
	buildOrder []string
	builds     map[string]*strings.Builder
	buildFail  map[string]bool
}

func appendCapped(b *strings.Builder, s string) {
	if b.Len()+len(s) <= adapterkit.MaxOutput {
		b.WriteString(s)
	}
}

func parseEvents(data []byte) []event {
	var out []event
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Action != "" {
			out = append(out, e)
		}
	}
	return out
}

func (c *converter) pkg(name string) *pkgState {
	p, ok := c.pkgs[name]
	if !ok {
		p = &pkgState{tests: map[string]*testState{}}
		c.pkgs[name] = p
		c.order = append(c.order, name)
	}
	return p
}

func (c *converter) build(e event) {
	b, ok := c.builds[e.ImportPath]
	if !ok {
		b = &strings.Builder{}
		c.builds[e.ImportPath] = b
		c.buildOrder = append(c.buildOrder, e.ImportPath)
	}
	if e.Action == "build-fail" {
		c.buildFail[e.ImportPath] = true
		return
	}
	appendCapped(b, e.Output)
}

func (c *converter) add(e event) {
	if strings.HasPrefix(e.Action, "build-") {
		c.build(e)
		return
	}
	p := c.pkg(e.Package)
	if e.Test == "" {
		switch e.Action {
		case "output":
			appendCapped(&p.out, e.Output)
		case "pass", "fail", "skip":
			p.action, p.failedBuild = e.Action, e.FailedBuild
		}
		return
	}
	t, ok := p.tests[e.Test]
	if !ok {
		t = &testState{}
		p.tests[e.Test] = t
		p.order = append(p.order, e.Test)
	}
	switch e.Action {
	case "output":
		appendCapped(&t.out, e.Output)
	case "pass", "fail", "skip":
		t.action = e.Action
	}
}

func (c *converter) suite(name string) (adapterkit.XMLSuite, string) {
	p := c.pkgs[name]
	s := adapterkit.XMLSuite{Name: name}
	failed := false
	for _, tn := range p.order {
		t := p.tests[tn]
		tc := adapterkit.XMLCase{Name: tn, Classname: name}
		switch t.action {
		case "fail":
			failed = true
			tc.Failure = &adapterkit.XMLText{Message: "test failed", Body: t.out.String()}
		case "skip":
			tc.Skipped = &struct{}{}
		case "pass":
		default:
			continue
		}
		s.Cases = append(s.Cases, tc)
	}
	if p.action == "fail" && !failed {
		body := p.out.String()
		if b, ok := c.builds[p.failedBuild]; ok {
			body = b.String() + body
		}
		s.Cases = append(s.Cases, adapterkit.XMLCase{Name: "(package)", Classname: name, Error: &adapterkit.XMLText{Message: "package failed without a failing test", Body: body}})
	}
	s.Tests = len(s.Cases)
	return s, p.failedBuild
}

func toJUnit(events []event) adapterkit.XMLSuites {
	c := &converter{pkgs: map[string]*pkgState{}, builds: map[string]*strings.Builder{}, buildFail: map[string]bool{}}
	for _, e := range events {
		c.add(e)
	}
	out := adapterkit.XMLSuites{Suites: []adapterkit.XMLSuite{}}
	covered := map[string]bool{}
	for _, name := range c.order {
		s, fb := c.suite(name)
		covered[fb] = true
		out.Suites = append(out.Suites, s)
	}
	for _, ip := range c.buildOrder {
		if c.buildFail[ip] && !covered[ip] {
			out.Suites = append(out.Suites, adapterkit.XMLSuite{Name: ip, Tests: 1, Cases: []adapterkit.XMLCase{{Name: "(build)", Classname: ip,
				Error: &adapterkit.XMLText{Message: "build failed", Body: c.builds[ip].String()}}}})
		}
	}
	return out
}

func errorSuite(name, msg string) adapterkit.XMLSuite {
	return adapterkit.XMLSuite{Name: name, Tests: 1, Cases: []adapterkit.XMLCase{{Name: "(run)", Classname: name, Error: &adapterkit.XMLText{Message: "go test did not run", Body: msg}}}}
}
