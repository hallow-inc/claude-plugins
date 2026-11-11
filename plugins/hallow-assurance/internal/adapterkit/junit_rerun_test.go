package adapterkit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

// Set by junit_rerun_wire_test.go once adapterkit.Rerun exists; delete that file's build
// constraint to wire it. Until then every rerun test fails instead of the package not compiling.
var rerunUnderTest func(cases []XMLCase, rerun func(failing []XMLCase) ([]XMLCase, error)) []XMLCase

const protocolRounds = 3

type fatalf interface{ Fatalf(string, ...any) }

func runRerun(t fatalf, cases []XMLCase, rerun func([]XMLCase) ([]XMLCase, error)) []XMLCase {
	if rerunUnderTest == nil {
		t.Fatalf("adapterkit.Rerun does not exist, so a test that fails once and passes on rerun reaches the Stop hook as a plain failure and the agent is sent after a flake it did not cause")
	}
	return rerunUnderTest(cases, rerun)
}

func caseProperties(t fatalf, c XMLCase) []XMLProperty {
	f := reflect.ValueOf(c).FieldByName("Properties")
	if !f.IsValid() || f.Type() != reflect.TypeFor[[]XMLProperty]() {
		t.Fatalf("XMLCase has no `Properties []XMLProperty` field, so an adapter cannot write assure.flaky on a testcase")
	}
	return f.Interface().([]XMLProperty)
}

func withoutProperties(c XMLCase) XMLCase {
	if f := reflect.ValueOf(&c).Elem().FieldByName("Properties"); f.IsValid() {
		f.SetZero()
	}
	return c
}

type caseKey struct{ classname, name string }

func keyOf(c XMLCase) caseKey { return caseKey{c.Classname, c.Name} }

func keysOf(cs []XMLCase) []caseKey {
	out := make([]caseKey, len(cs))
	for i, c := range cs {
		out[i] = keyOf(c)
	}
	return out
}

func sameKeySet(a, b []caseKey) bool {
	cmp := func(x, y caseKey) int {
		if x.classname != y.classname {
			if x.classname < y.classname {
				return -1
			}
			return 1
		}
		if x.name < y.name {
			return -1
		}
		if x.name > y.name {
			return 1
		}
		return 0
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.SortFunc(a, cmp)
	slices.SortFunc(b, cmp)
	return slices.Equal(a, b)
}

type outcome int

const (
	rerunPasses outcome = iota
	rerunFails
	rerunErrors
	rerunSkips
	rerunMissing
)

func rerunResult(k caseKey, o outcome) (XMLCase, bool) {
	c := XMLCase{Classname: k.classname, Name: k.name}
	switch o {
	case rerunFails:
		c.Failure = &XMLText{Message: "rerun failure", Body: "rerun trace"}
	case rerunErrors:
		c.Error = &XMLText{Message: "rerun error", Body: "rerun build output"}
	case rerunSkips:
		c.Skipped = &struct{}{}
	case rerunMissing:
		return c, false
	}
	return c, true
}

type fakeRerun struct {
	t        fatalf
	script   map[caseKey][]outcome
	roundErr []bool
	batches  [][]caseKey
}

func (f *fakeRerun) rerun(failing []XMLCase) ([]XMLCase, error) {
	f.batches = append(f.batches, keysOf(failing))
	round := len(f.batches)
	if round > protocolRounds {
		f.t.Fatalf("rerun called %d times; the protocol allows at most %d rerun rounds, and each one costs Stop-hook time", round, protocolRounds)
	}
	var out []XMLCase
	for _, c := range failing {
		o := rerunFails
		if s, ok := f.script[keyOf(c)]; ok && round <= len(s) {
			o = s[round-1]
		}
		if r, ok := rerunResult(keyOf(c), o); ok {
			out = append(out, r)
		}
	}
	if round <= len(f.roundErr) && f.roundErr[round-1] {
		return out, errors.New("rerun process exited 2")
	}
	return out, nil
}

func marshalCase(t fatalf, c XMLCase) []byte {
	b, err := xml.Marshal(c)
	if err != nil {
		t.Fatalf("marshal %+v: %v", c, err)
	}
	return b
}

func parseRerunOutput(t fatalf, cases []XMLCase) (ev.JUnit, []byte) {
	data, err := xml.Marshal(XMLSuites{Suites: []XMLSuite{{Name: "s", Tests: len(cases), Cases: cases}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j, err := ev.ParseJUnit(append([]byte(xml.Header), data...))
	if err != nil {
		t.Fatalf("assure's JUnit reader rejects the rerun output: %v\n%s", err, data)
	}
	return j, data
}

func failingNamed(t fatalf, j ev.JUnit, name string, data []byte) ev.Failure {
	for _, f := range j.Failing {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s is not among the failures assure read; a rerun must never turn a first-run failure into a pass\n%s", name, data)
	return ev.Failure{}
}

func TestRerunMarksAFlakeThatPassesOnTheSecondRerunWithThatRound(t *testing.T) {
	first := XMLCase{Classname: "pkg/x", Name: "TestFlake", Failure: &XMLText{Message: "first-run failure", Body: "first-run trace"}}
	f := &fakeRerun{t: t, script: map[caseKey][]outcome{keyOf(first): {rerunFails, rerunPasses}}}
	got := runRerun(t, []XMLCase{first}, f.rerun)

	if len(f.batches) != 2 {
		t.Fatalf("rerun called %d times, want 2: the case passed on the second rerun and must stop being rerun", len(f.batches))
	}
	if len(got) != 1 {
		t.Fatalf("Rerun returned %d cases, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0].Failure, first.Failure) {
		t.Fatalf("Failure = %+v, want the first run's %+v; the flake must still fail the check with what was first observed", got[0].Failure, first.Failure)
	}
	if p := caseProperties(t, got[0]); !slices.Equal(p, []XMLProperty{{Name: "assure.flaky", Value: "2"}}) {
		t.Fatalf("properties = %+v, want exactly assure.flaky=2; the report names the rerun that passed", p)
	}
	j, data := parseRerunOutput(t, got)
	if j.Failures != 1 || len(j.Passed) != 0 {
		t.Fatalf("assure read %+v; a flake is still a failure, never a pass\n%s", j, data)
	}
	if r := failingNamed(t, j, "pkg/x.TestFlake", data).Flaky; r != 2 {
		t.Fatalf("assure read Flaky = %d, want 2\n%s", r, data)
	}
}

func TestRerunLeavesACaseThatFailsEveryRoundAsTheFirstRunWroteIt(t *testing.T) {
	first := XMLCase{Classname: "pkg/x", Name: "TestBroken", Failure: &XMLText{Message: "first-run failure", Body: "first-run trace"}}
	want := marshalCase(t, first)
	f := &fakeRerun{t: t}
	got := runRerun(t, []XMLCase{first}, f.rerun)

	if len(f.batches) != protocolRounds {
		t.Fatalf("rerun called %d times, want %d: a test that keeps failing runs exactly 4 times, first run included", len(f.batches), protocolRounds)
	}
	for i, b := range f.batches {
		if !slices.Equal(b, []caseKey{keyOf(first)}) {
			t.Fatalf("round %d reran %v, want only %v", i+1, b, keyOf(first))
		}
	}
	if len(got) != 1 || !bytes.Equal(marshalCase(t, got[0]), want) {
		t.Fatalf("Rerun returned %+v, want the first-run case unchanged; a real failure must reach the agent as it was observed", got)
	}
	j, data := parseRerunOutput(t, got)
	if r := failingNamed(t, j, "pkg/x.TestBroken", data).Flaky; r != 0 {
		t.Fatalf("assure read Flaky = %d for a test that failed every round; the agent would be told a real failure is not its to fix\n%s", r, data)
	}
}

func TestRerunNeverRerunsPassingTests(t *testing.T) {
	cases := []XMLCase{}
	for i := range 10 {
		c := XMLCase{Classname: "unit", Name: "TestPass" + strconv.Itoa(i)}
		if i == 4 {
			c = XMLCase{Classname: "unit", Name: "TestRed", Failure: &XMLText{Message: "red"}}
		}
		cases = append(cases, c)
	}
	f := &fakeRerun{t: t}
	got := runRerun(t, slices.Clone(cases), f.rerun)

	if len(f.batches) != protocolRounds {
		t.Fatalf("rerun called %d times, want %d for the one always-failing test", len(f.batches), protocolRounds)
	}
	for i, b := range f.batches {
		if !slices.Equal(b, []caseKey{{"unit", "TestRed"}}) {
			t.Fatalf("round %d reran %v; each passing test must run exactly once, and a passing peer beside a rerun could mask the failure it shares state with", i+1, b)
		}
	}
	if len(got) != len(cases) {
		t.Fatalf("Rerun returned %d cases, want %d", len(got), len(cases))
	}
	for i := range cases {
		if !bytes.Equal(marshalCase(t, got[i]), marshalCase(t, cases[i])) {
			t.Fatalf("case %d = %+v, want %+v unchanged", i, got[i], cases[i])
		}
	}
}

func TestRerunNeverRerunsABuildError(t *testing.T) {
	build := XMLCase{Classname: "pkg/x", Name: "build", Error: &XMLText{Message: "build failed", Body: "undefined: Foo"}}
	f := &fakeRerun{t: t, script: map[caseKey][]outcome{keyOf(build): {rerunPasses, rerunPasses, rerunPasses}}}
	got := runRerun(t, []XMLCase{build}, f.rerun)

	if len(f.batches) != 0 {
		t.Fatalf("rerun called with %v; a package that failed to build has no test to rerun, and a rerun that builds could hide the break", f.batches)
	}
	if len(got) != 1 || !bytes.Equal(marshalCase(t, got[0]), marshalCase(t, build)) {
		t.Fatalf("Rerun returned %+v, want the erroring case unchanged", got)
	}
	j, data := parseRerunOutput(t, got)
	if f := failingNamed(t, j, "pkg/x.build", data); !f.Error || f.Flaky != 0 {
		t.Fatalf("assure read %+v, want an unflaky error\n%s", f, data)
	}
}

type firstRunKind int

const (
	firstPasses firstRunKind = iota
	firstSkips
	firstFails
	firstErrors
	firstFailsAndErrors
)

type drawnCase struct {
	c        XMLCase
	kind     firstRunKind
	outcomes []outcome
}

func drawRerunCase(t *rapid.T) drawnCase {
	d := drawnCase{
		c: XMLCase{
			Classname: rapid.SampledFrom([]string{"p", "q/r", ""}).Draw(t, "classname"),
			Name:      rapid.SampledFrom([]string{"TestA", "TestB", "TestC", "TestD", "TestE"}).Draw(t, "name"),
		},
		kind: firstRunKind(rapid.IntRange(int(firstPasses), int(firstFailsAndErrors)).Draw(t, "kind")),
	}
	text := &XMLText{Message: xmlSafeText().Draw(t, "message"), Body: xmlSafeText().Draw(t, "body")}
	switch d.kind {
	case firstSkips:
		d.c.Skipped = &struct{}{}
	case firstFails:
		d.c.Failure = text
	case firstErrors:
		d.c.Error = text
	case firstFailsAndErrors:
		d.c.Failure = text
		d.c.Error = &XMLText{Message: "teardown panic"}
	}
	d.outcomes = rapid.SliceOfN(rapid.SampledFrom([]outcome{rerunPasses, rerunFails, rerunErrors, rerunSkips, rerunMissing}), protocolRounds, protocolRounds).Draw(t, "outcomes")
	return d
}

type rerunModel struct {
	batches [][]caseKey
	flaky   map[caseKey]int
}

func modelRerun(drawn []drawnCase, roundErr []bool) rerunModel {
	m := rerunModel{flaky: map[caseKey]int{}}
	for round := 1; round <= protocolRounds; round++ {
		var batch []drawnCase
		for _, d := range drawn {
			if _, done := m.flaky[keyOf(d.c)]; d.kind == firstFails && !done {
				batch = append(batch, d)
			}
		}
		if len(batch) == 0 {
			break
		}
		keys := make([]caseKey, len(batch))
		for i, d := range batch {
			keys[i] = keyOf(d.c)
			if d.outcomes[round-1] == rerunPasses && !roundErr[round-1] {
				m.flaky[keys[i]] = round
			}
		}
		m.batches = append(m.batches, keys)
	}
	return m
}

func checkRerunBatches(t *rapid.T, got, want [][]caseKey) {
	if len(got) != len(want) {
		t.Fatalf("rerun called %d times with %v, want %d with %v: rounds run only while a first-run failure is still failing, and never more than %d", len(got), got, len(want), want, protocolRounds)
	}
	for i := range want {
		if !sameKeySet(got[i], want[i]) {
			t.Fatalf("round %d reran %v, want %v: each round reruns exactly the first-run failures still failing; a passing, skipped or erroring case is never rerun", i+1, got[i], want[i])
		}
	}
}

func checkRerunCases(t *rapid.T, drawn []drawnCase, before [][]byte, got []XMLCase, m rerunModel) {
	if len(got) != len(drawn) {
		t.Fatalf("Rerun returned %d cases for %d", len(got), len(drawn))
	}
	for i, d := range drawn {
		k := keyOf(d.c)
		if keyOf(got[i]) != k {
			t.Fatalf("case %d is %v, want %v: output keeps the input order", i, keyOf(got[i]), k)
		}
		round, flaky := m.flaky[k]
		if !flaky {
			if after := marshalCase(t, got[i]); !bytes.Equal(after, before[i]) {
				t.Fatalf("case %v (kind %d, outcomes %v) changed from\n%s\nto\n%s\nonly a case that passed a clean rerun round may change; anything ambiguous stays a plain failure", k, d.kind, d.outcomes, before[i], after)
			}
			continue
		}
		if p := caseProperties(t, got[i]); !slices.Equal(p, []XMLProperty{{Name: "assure.flaky", Value: strconv.Itoa(round)}}) {
			t.Fatalf("case %v (outcomes %v) has properties %+v, want exactly assure.flaky=%d", k, d.outcomes, p, round)
		}
		if after := marshalCase(t, withoutProperties(got[i])); !bytes.Equal(after, before[i]) {
			t.Fatalf("flaky case %v changed beyond its property, from\n%s\nto\n%s\nthe first run's <failure> must be kept", k, before[i], after)
		}
	}
}

func checkRerunEvidence(t *rapid.T, drawn []drawnCase, got []XMLCase, m rerunModel) {
	j, data := parseRerunOutput(t, got)
	for _, d := range drawn {
		if d.c.Failure == nil && d.c.Error == nil {
			continue
		}
		name := d.c.Name
		if d.c.Classname != "" {
			name = d.c.Classname + "." + d.c.Name
		}
		if r := failingNamed(t, j, name, data).Flaky; r != m.flaky[keyOf(d.c)] {
			t.Fatalf("assure read Flaky = %d for %s, want %d\n%s", r, name, m.flaky[keyOf(d.c)], data)
		}
	}
}

func TestRerunPropertyFlakyValueIsTheFirstPassingRoundAndNothingElseChanges(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		drawn := rapid.SliceOfNDistinct(rapid.Custom(drawRerunCase), 0, 10, func(d drawnCase) caseKey { return keyOf(d.c) }).Draw(t, "cases")
		roundErr := rapid.SliceOfN(rapid.Bool(), protocolRounds, protocolRounds).Draw(t, "roundErr")
		strays := rapid.Bool().Draw(t, "strays")

		in := make([]XMLCase, len(drawn))
		before := make([][]byte, len(drawn))
		script := map[caseKey][]outcome{}
		for i, d := range drawn {
			in[i] = d.c
			before[i] = marshalCase(t, d.c)
			script[keyOf(d.c)] = d.outcomes
		}

		f := &fakeRerun{t: t, script: script, roundErr: roundErr}
		fake := func(failing []XMLCase) ([]XMLCase, error) {
			out, err := f.rerun(failing)
			if strays {
				for _, d := range drawn {
					if d.kind != firstFails {
						out = append(out, XMLCase{Classname: d.c.Classname, Name: d.c.Name})
					}
				}
				out = append(out, XMLCase{Classname: "stray", Name: "TestNotSelected"})
			}
			return rapid.Permutation(out).Draw(t, "result order"), err
		}
		got := runRerun(t, in, fake)

		m := modelRerun(drawn, roundErr)
		checkRerunBatches(t, f.batches, m.batches)
		checkRerunCases(t, drawn, before, got, m)
		checkRerunEvidence(t, drawn, got, m)
	})
}
