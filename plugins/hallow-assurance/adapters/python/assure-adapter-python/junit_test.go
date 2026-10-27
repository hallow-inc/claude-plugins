package main

import (
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

type pyXMLText struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type pyXMLCase struct {
	Classname string     `xml:"classname,attr"`
	Name      string     `xml:"name,attr"`
	File      string     `xml:"file,attr"`
	Line      string     `xml:"line,attr,omitempty"`
	Time      string     `xml:"time,attr"`
	Failure   *pyXMLText `xml:"failure"`
	Error     *pyXMLText `xml:"error"`
	Skipped   *pyXMLText `xml:"skipped"`
}

type pyXMLSuite struct {
	Name     string      `xml:"name,attr"`
	Errors   int         `xml:"errors,attr"`
	Failures int         `xml:"failures,attr"`
	Tests    int         `xml:"tests,attr"`
	Cases    []pyXMLCase `xml:"testcase"`
}

type pyXMLReport struct {
	XMLName xml.Name     `xml:"testsuites"`
	Name    string       `xml:"name,attr"`
	Suites  []pyXMLSuite `xml:"testsuite"`
}

// wantCase is what assure must read back for one generated pytest result:
// classname is the file relative to the manifest root, name the node id
// after the file.
type wantCase struct {
	classname, name string
	outcome         ev.Outcome
	message, text   string
	textHas         []string
}

func (w wantCase) key() string { return w.classname + "." + w.name }

type junitModel struct {
	top, unit, root, rootdir string
	cases                    []pyXMLCase
	want                     map[string]wantCase
	failing                  int
}

var (
	unitRelGen    = rapid.SampledFrom([]string{".", "svc", "svc/api"})
	testDirGen    = rapid.SampledFrom([]string{"tests", "tests/sub", "pkg.v2/tests", "."})
	testFileGen   = rapid.SampledFrom([]string{"test_a.py", "test_b.py", "c_test.py"})
	classGen      = rapid.SliceOfN(rapid.SampledFrom([]string{"TestK", "TestInner", "TestÉ"}), 0, 2)
	testNameGen   = rapid.SampledFrom([]string{"test_x", "test_y", "test_é", "test"})
	paramGen      = rapid.SampledFrom([]string{"", "[1]", "[a.b]", "[c::d]", "[x/y]", "[1-2]", "[<&>]"})
	outcomeGen    = rapid.SampledFrom([]ev.Outcome{ev.Passed, ev.Passed, ev.Failed, ev.Errored, ev.Skipped})
	bodyGen       = rapid.SampledFrom([]string{"", "  \n", "E   assert 1 == 2", "  lead <tag> & \"q\" é\n\ttab\n", "x = 1\n\n>   assert x\nE   AssertionError\n\ntests/t.py:3: AssertionError"})
	messageGen    = rapid.SampledFrom([]string{"assert False", "failed on setup with \"RuntimeError: boom\"", "<&> é", ""})
	collectDirGen = rapid.SampledFrom([]string{"tests/broken", "tests/sub"})
)

func dotted(file string) string {
	return strings.ReplaceAll(strings.TrimSuffix(file, ".py"), "/", ".")
}

// drawLayout places the unit under top, the manifest root at or above the
// unit, and pytest's rootdir at the unit or its parent, as an ini file in a
// parent directory makes it.
func drawLayout(t *rapid.T, top string) *junitModel {
	m := &junitModel{top: top, want: map[string]wantCase{}}
	unitRel := unitRelGen.Draw(t, "unit")
	m.unit = filepath.Join(top, filepath.FromSlash(unitRel))
	var ancestors []string
	for d := unitRel; ; d = path.Dir(d) {
		ancestors = append(ancestors, d)
		if d == "." {
			break
		}
	}
	m.root = filepath.Join(top, filepath.FromSlash(rapid.SampledFrom(ancestors).Draw(t, "root")))
	m.rootdir = m.unit
	if len(ancestors) > 1 && rapid.Bool().Draw(t, "parentRootdir") {
		m.rootdir = filepath.Join(top, filepath.FromSlash(ancestors[1]))
	}
	return m
}

func (m *junitModel) place(t *rapid.T, unitRel string, dir bool) (fileAttr, classname string) {
	abs := filepath.Join(m.unit, filepath.FromSlash(unitRel))
	if dir {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			t.Fatalf("%v", err)
		}
	} else {
		writeTree(t, m.unit, map[string]string{unitRel: ""})
	}
	return relSlash(m.rootdir, abs), relSlash(m.root, abs)
}

func (m *junitModel) addCollectionError(t *rapid.T, unitRel string, dir bool) {
	file, classname := m.place(t, unitRel, dir)
	body := bodyGen.Draw(t, "collectBody")
	w := wantCase{classname: classname, name: "(collection)", outcome: ev.Errored, message: "collection failure", text: expectText(body)}
	if _, dup := m.want[w.key()]; dup {
		return
	}
	m.want[w.key()] = w
	m.failing++
	m.cases = append(m.cases, pyXMLCase{Classname: "", Name: dotted(file), File: file, Time: "0.000", Error: &pyXMLText{Message: "collection failure", Body: body}})
}

func (m *junitModel) addTest(t *rapid.T, unitRel string) {
	file, classname := m.place(t, unitRel, false)
	classes := classGen.Draw(t, "classes")
	name := testNameGen.Draw(t, "test") + paramGen.Draw(t, "param")
	w := wantCase{classname: classname, name: strings.Join(append(slices.Clone(classes), name), "::"), outcome: outcomeGen.Draw(t, "outcome")}
	if _, dup := m.want[w.key()]; dup {
		return
	}
	c := pyXMLCase{Classname: strings.Join(append([]string{dotted(file)}, classes...), "."), Name: name, File: file, Line: "3", Time: "0.001"}
	body, msg := bodyGen.Draw(t, "body"), messageGen.Draw(t, "message")
	switch w.outcome {
	case ev.Failed:
		c.Failure = &pyXMLText{Message: msg, Body: body}
		w.message, w.text = msg, expectText(body)
		m.failing++
	case ev.Errored:
		c.Error = &pyXMLText{Message: msg, Body: body}
		w.message, w.text = msg, expectText(body)
		m.failing++
	case ev.Skipped:
		c.Skipped = &pyXMLText{Type: rapid.SampledFrom([]string{"pytest.skip", "pytest.xfail"}).Draw(t, "skipType"), Message: msg, Body: body}
	}
	m.cases = append(m.cases, c)
	if (w.outcome == ev.Failed || w.outcome == ev.Skipped) && rapid.Bool().Draw(t, "teardownError") {
		w = m.addTeardownError(c, w)
	}
	m.want[w.key()] = w
}

const (
	teardownMessage = "failed on teardown with \"RuntimeError: teardown boom\""
	teardownBody    = "E   RuntimeError: teardown boom é"
)

// addTeardownError adds a teardown error to a test the way pytest 9 writes
// it: after a failed call, a second <testcase> of the same name holding the
// <error>; after a skip, an <error> beside the <skipped> in the one case.
func (m *junitModel) addTeardownError(c pyXMLCase, w wantCase) wantCase {
	td := &pyXMLText{Message: teardownMessage, Body: teardownBody}
	if w.outcome == ev.Skipped {
		m.cases[len(m.cases)-1].Error = td
		m.failing++
		w.outcome, w.message, w.text = ev.Errored, teardownMessage, teardownBody
		return w
	}
	second := c
	second.Failure, second.Error = nil, td
	m.cases = append(m.cases, second)
	w.textHas = []string{teardownBody}
	if s := strings.TrimSpace(c.Failure.Body); s != "" {
		w.textHas = append(w.textHas, s)
	}
	return w
}

func expectText(body string) string {
	if s := strings.TrimSpace(body); s != "" {
		return s
	}
	return "no output"
}

// render writes the cases as pytest's xunit1 report does: one <testsuite>
// in <testsuites>, cases in run order, which pytest-randomly shuffles. A
// second suite is drawn too, since the reader must not assume one.
func (m *junitModel) render(t *rapid.T) []byte {
	order := rapid.Permutation(m.cases).Draw(t, "order")
	split := rapid.IntRange(0, len(order)).Draw(t, "split")
	r := pyXMLReport{Name: "pytest tests", Suites: []pyXMLSuite{{Name: "pytest", Cases: order[:split]}}}
	if split < len(order) || rapid.Bool().Draw(t, "emptySecond") {
		r.Suites = append(r.Suites, pyXMLSuite{Name: "pytest", Cases: order[split:]})
	}
	for i := range r.Suites {
		r.Suites[i].Tests = len(r.Suites[i].Cases)
	}
	data, err := xml.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatalf("%v", err)
	}
	return append([]byte(xml.Header), data...)
}

type writtenSuite struct {
	Name  string `xml:"name,attr"`
	Tests int    `xml:"tests,attr"`
	Cases []struct {
		Name      string `xml:"name,attr"`
		Classname string `xml:"classname,attr"`
	} `xml:"testcase"`
}

// TestConversionPreservesFailures is the spec scenario "pytest conversion
// preserves failures" over generated xunit1 reports. VER-TESTS-PASS passes
// on zero failures plus errors, so a dropped collection error or failure
// turns a broken suite green; the names are what fail-on-base and the
// report show, so they must be the node id after a root-relative file.
func TestConversionPreservesFailures(t *testing.T) {
	base := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		m := drawLayout(t, top)
		for range rapid.IntRange(0, 6).Draw(t, "tests") {
			m.addTest(t, path.Join(testDirGen.Draw(t, "dir"), testFileGen.Draw(t, "file")))
		}
		for range rapid.IntRange(0, 2).Draw(t, "collectionErrors") {
			if rapid.Bool().Draw(t, "dirError") {
				m.addCollectionError(t, collectDirGen.Draw(t, "cdir"), true)
			} else {
				m.addCollectionError(t, path.Join(testDirGen.Draw(t, "dir"), "test_broken.py"), false)
			}
		}
		raw := m.render(t)
		in := filepath.Join(top, "pytest.xml")
		if err := os.WriteFile(in, raw, 0o644); err != nil {
			t.Fatalf("%v", err)
		}
		r, err := readPytestReport(in)
		if err != nil {
			t.Fatalf("readPytestReport: %v\n%s", err, raw)
		}
		out := filepath.Join(top, "junit.xml")
		if err := writeJUnit(out, toJUnit(m.root, m.top, m.unit, r)); err != nil {
			t.Fatalf("writeJUnit: %v", err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%v", err)
		}
		j, err := ev.ParseJUnit(data)
		if err != nil {
			t.Fatalf("assure's JUnit reader rejects the converted report: %v\n%s", err, data)
		}
		ctx := fmt.Sprintf("\npytest report:\n%s\nconverted:\n%s", raw, data)
		if got := j.Failures + j.Errors; got != m.failing {
			t.Fatalf("failures+errors = %d, want %d (failed tests plus files that failed to collect); VER-TESTS-PASS passes on zero, so a lost failure passes a broken suite%s", got, m.failing, ctx)
		}
		checkCases(t, j, m.want, ctx)
		checkSuiteShape(t, data, ctx)
	})
}

func checkCases(t *rapid.T, j ev.JUnit, want map[string]wantCase, ctx string) {
	got := map[string]ev.Outcome{}
	for _, s := range j.Suites {
		for _, c := range s.Cases {
			got[c.Name] = c.Outcome
		}
	}
	wantOutcomes := map[string]ev.Outcome{}
	for k, w := range want {
		wantOutcomes[k] = w.outcome
	}
	if !maps.Equal(got, wantOutcomes) {
		t.Fatalf("cases\ngot  %v\nwant %v\nclassname must be the file relative to the manifest root (pytest writes it relative to its rootdir), the name the node id after the file (Class::test[param]), and a file that fails to collect one erroring (collection) case%s", got, wantOutcomes, ctx)
	}
	for _, f := range j.Failing {
		w := want[f.Name]
		if w.textHas != nil {
			for _, part := range w.textHas {
				if f.Message != w.message || !strings.Contains(f.Text, part) {
					t.Fatalf("%s: message %q text %q, want message %q and text containing %q; a test that failed and then errored in teardown is one failed case that keeps both tracebacks%s", f.Name, f.Message, f.Text, w.message, part, ctx)
				}
			}
			continue
		}
		if f.Message != w.message || f.Text != w.text {
			t.Fatalf("%s: message %q text %q, want %q %q; the developer reads pytest's message and traceback (trimmed) here, and a collection error must carry the tool's output for that file%s", f.Name, f.Message, f.Text, w.message, w.text, ctx)
		}
	}
	if j.Cases != len(want) {
		t.Fatalf("%d cases, want %d%s", j.Cases, len(want), ctx)
	}
}

// checkSuiteShape asserts one suite per file named by it, suites sorted by
// file and cases by name, so evidence from the same results is byte-stable
// whatever order pytest-randomly ran them in.
func checkSuiteShape(t *rapid.T, data []byte, ctx string) {
	var w struct {
		Suites []writtenSuite `xml:"testsuite"`
	}
	if err := xml.Unmarshal(data, &w); err != nil {
		t.Fatalf("%v", err)
	}
	var names []string
	for _, s := range w.Suites {
		names = append(names, s.Name)
		if s.Tests != len(s.Cases) {
			t.Fatalf("suite %s tests=%d with %d cases%s", s.Name, s.Tests, len(s.Cases), ctx)
		}
		var cn []string
		for _, c := range s.Cases {
			if c.Classname != s.Name {
				t.Fatalf("case %s has classname %s in suite %s; each suite holds one file's cases%s", c.Name, c.Classname, s.Name, ctx)
			}
			cn = append(cn, c.Name)
		}
		if !slices.IsSorted(cn) {
			t.Fatalf("suite %s cases %q not sorted by name; the evidence must not depend on the shuffled run order%s", s.Name, cn, ctx)
		}
	}
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Fatalf("suites %q, want one per file sorted by file%s", names, ctx)
	}
}

func TestConversionIsIndependentOfRunOrder(t *testing.T) {
	base := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "top")
		if err != nil {
			t.Fatalf("%v", err)
		}
		m := drawLayout(t, top)
		for range rapid.IntRange(1, 6).Draw(t, "tests") {
			m.addTest(t, path.Join(testDirGen.Draw(t, "dir"), testFileGen.Draw(t, "file")))
		}
		var outs [2][]byte
		for i := range outs {
			in, out := filepath.Join(top, fmt.Sprint(i, ".xml")), filepath.Join(top, fmt.Sprint(i, "-junit.xml"))
			if err := os.WriteFile(in, m.render(t), 0o644); err != nil {
				t.Fatalf("%v", err)
			}
			r, err := readPytestReport(in)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if err := writeJUnit(out, toJUnit(m.root, m.top, m.unit, r)); err != nil {
				t.Fatalf("%v", err)
			}
			if outs[i], err = os.ReadFile(out); err != nil {
				t.Fatalf("%v", err)
			}
		}
		if string(outs[0]) != string(outs[1]) {
			t.Fatalf("two run orders of the same results converted differently:\n%s\n----\n%s\nthe seed shuffles pytest's order per commit, and evidence must not change with it", outs[0], outs[1])
		}
	})
}

func TestReadPytestReportFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"garbage.xml": "not xml", "truncated.xml": "<testsuites><testsuite><testcase name=\"x\""})
	for _, name := range []string{"missing.xml", "garbage.xml", "truncated.xml"} {
		if _, err := readPytestReport(filepath.Join(dir, name)); err == nil {
			t.Fatalf("readPytestReport(%s) = nil error; a report that cannot be read must stop the run, not count as zero failures", name)
		}
	}
}
