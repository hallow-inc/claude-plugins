package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

// fobTest is one generated test: its identity parts and the two things
// that make up its text, a decorator and a body.
type fobTest struct {
	class, name string
	deco, body  int
}

var (
	fobNames   = []string{"test_a", "test_b", "test_c", "test_é"}
	fobClasses = []string{"", "", "TestK", "TestL"}
	fobDecos   = []string{"", "@pytest.mark.slow\n", "@pytest.mark.parametrize(\"x\", [1, 2])\n"}
	fobBodies  = []string{"assert 1", "assert 2", "x = 'é'\nassert x"}
)

func (t fobTest) key() string { return t.class + "::" + t.name }

// renderFob writes tests grouped by class in the given order; classAttr
// changes class-level code outside the methods, which selects nothing.
func renderFob(tests []fobTest, classAttr int) string {
	var b strings.Builder
	b.WriteString("import pytest\n\n\ndef helper():\n    return 1\n\n")
	byClass := map[string][]fobTest{}
	var classes []string
	for _, t := range tests {
		if _, ok := byClass[t.class]; !ok {
			classes = append(classes, t.class)
		}
		byClass[t.class] = append(byClass[t.class], t)
	}
	for _, c := range classes {
		indent := ""
		if c != "" {
			fmt.Fprintf(&b, "\nclass %s:\n    attr = %d\n", c, classAttr)
			indent = "    "
		}
		for _, t := range byClass[c] {
			b.WriteString("\n")
			if d := fobDecos[t.deco]; d != "" {
				b.WriteString(indent + d)
			}
			self := ""
			if c != "" {
				self = "self"
			}
			fmt.Fprintf(&b, "%sdef %s(%s):\n", indent, t.name, self)
			for l := range strings.SplitSeq(fobBodies[t.body], "\n") {
				b.WriteString(indent + "    " + l + "\n")
			}
		}
	}
	return b.String()
}

func drawFobTests(t *rapid.T, label string) []fobTest {
	seen := map[string]bool{}
	var out []fobTest
	for range rapid.IntRange(0, 5).Draw(t, label+"N") {
		ft := fobTest{
			class: rapid.SampledFrom(fobClasses).Draw(t, label+"Class"),
			name:  rapid.SampledFrom(fobNames).Draw(t, label+"Name"),
			deco:  rapid.IntRange(0, len(fobDecos)-1).Draw(t, label+"Deco"),
			body:  rapid.IntRange(0, len(fobBodies)-1).Draw(t, label+"Body"),
		}
		if !seen[ft.key()] {
			seen[ft.key()] = true
			out = append(out, ft)
		}
	}
	return out
}

// editFob derives the head version of a file: each base test is kept,
// re-decorated, re-bodied or deleted, new tests may be added, and the order
// may change.
func editFob(t *rapid.T, base []fobTest) []fobTest {
	var head []fobTest
	seen := map[string]bool{}
	for _, b := range base {
		switch rapid.SampledFrom([]string{"keep", "keep", "deco", "body", "delete"}).Draw(t, "edit") {
		case "keep":
		case "deco":
			b.deco = (b.deco + 1) % len(fobDecos)
		case "body":
			b.body = (b.body + 1) % len(fobBodies)
		case "delete":
			continue
		}
		seen[b.key()] = true
		head = append(head, b)
	}
	for _, n := range drawFobTests(t, "added") {
		if !seen[n.key()] {
			seen[n.key()] = true
			head = append(head, n)
		}
	}
	return rapid.Permutation(head).Draw(t, "order")
}

func fobID(file string, ft fobTest) string {
	if ft.class == "" {
		return file + "::" + ft.name
	}
	return file + "::" + ft.class + "::" + ft.name
}

// TestSelectChangedMatchesModel is task 8.1: a test is selected exactly
// when its identity is new or its text (decorators included) changed.
// Selecting an unchanged test makes fail-on-base demand that an old,
// passing test fail; missing an edited one lets a rewritten test skip the
// check. Class-level code, order, conftest.py and deleted files select
// nothing.
func TestSelectChangedMatchesModel(t *testing.T) {
	py := testPython(t)
	base := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		dir := tempIn(t, base)
		root, baseRoot := filepath.Join(dir, "root"), filepath.Join(dir, "base")
		var files []string
		var want []string
		for _, f := range []string{"tests/test_a.py", "tests/sub/test_b.py"} {
			state := rapid.SampledFrom([]string{"edited", "edited", "added", "deleted"}).Draw(t, "state")
			baseTests := drawFobTests(t, "base")
			headTests := editFob(t, baseTests)
			files = append(files, f)
			if state != "added" {
				writeTree(t, baseRoot, map[string]string{f: renderFob(baseTests, 1)})
			}
			if state == "deleted" {
				continue
			}
			writeTree(t, root, map[string]string{f: renderFob(headTests, rapid.IntRange(1, 2).Draw(t, "attr"))})
			old := map[string]fobTest{}
			if state == "edited" {
				for _, b := range baseTests {
					old[b.key()] = b
				}
			}
			for _, h := range headTests {
				if b, ok := old[h.key()]; !ok || b != h {
					want = append(want, fobID(f, h))
				}
			}
		}
		conftest := renderFob([]fobTest{{name: "test_a"}}, 1)
		writeTree(t, baseRoot, map[string]string{"tests/conftest.py": conftest})
		writeTree(t, root, map[string]string{"tests/conftest.py": conftest + "\ndef test_new():\n    pass\n"})
		files = append(files, "tests/conftest.py")
		selected, unparsed, err := selectChanged(py, root, baseRoot, files)
		if err != nil {
			t.Fatalf("selectChanged: %v", err)
		}
		if len(unparsed) != 0 {
			t.Fatalf("unparsed %+v; every generated head file parses", unparsed)
		}
		var got []string
		for _, s := range selected {
			if !strings.HasPrefix(s.id, s.file+"::") {
				t.Fatalf("selected %+v: id does not start with its file", s)
			}
			got = append(got, s.id)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("selected %q\nwant     %q\nD9: a test is selected when its identity is new or its text, decorators included, differs; class-level code, order, conftest.py and deleted files select nothing", got, want)
		}
	})
}

func TestSelectChangedUnparseableHeadFile(t *testing.T) {
	py := testPython(t)
	dir := t.TempDir()
	root, baseRoot := filepath.Join(dir, "root"), filepath.Join(dir, "base")
	writeTree(t, baseRoot, map[string]string{"tests/test_x.py": "def test_x():\n    pass\n", "tests/test_ok.py": "def test_ok():\n    pass\n"})
	writeTree(t, root, map[string]string{"tests/test_x.py": "def test_x(:\n    pass\n", "tests/test_ok.py": "def test_ok():\n    assert 1\n"})
	selected, unparsed, err := selectChanged(py, root, baseRoot, []string{"tests/test_ok.py", "tests/test_x.py"})
	if err != nil {
		t.Fatalf("selectChanged: %v; one unparseable file must not stop the others", err)
	}
	if len(selected) != 1 || selected[0].id != "tests/test_ok.py::test_ok" {
		t.Fatalf("selected %+v, want tests/test_ok.py::test_ok", selected)
	}
	if len(unparsed) != 1 || unparsed[0].Classname != "tests/test_x.py" || unparsed[0].Error == nil || !strings.Contains(unparsed[0].Error.Body, "test_x.py") {
		t.Fatalf("unparsed %+v; a changed test file that does not parse has unknown tests, so it must be an erroring case carrying the parser's message, never zero selected tests", unparsed)
	}
}

func TestChangedTestFiles(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{
		".gitignore":                     "*.tmp.py\n",
		"svc/a/pyproject.toml":           "",
		"svc/a/app/mod.py":               "x = 1\n",
		"svc/a/tests/test_keep.py":       "def test_k():\n    pass\n",
		"svc/a/tests/test_edit.py":       "def test_e():\n    pass\n",
		"svc/a/tests/test_gone.py":       "def test_g():\n    pass\n",
		"svc/a/tests/conftest.py":        "",
		"svc/a/tests/sub/pyproject.toml": "",
		"svc/a/tests/sub/test_nested.py": "",
		"svc/b/pyproject.toml":           "",
		"svc/b/tests/test_other.py":      "",
	})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	writeTree(t, top, map[string]string{
		"svc/a/app/mod.py":                "x = 2\n",
		"svc/a/tests/test_edit.py":        "def test_e():\n    assert 1\n",
		"svc/a/tests/conftest.py":         "X = 1\n",
		"svc/a/tests/test_new.py":         "def test_n():\n    pass\n",
		"svc/a/tests/test_scratch.tmp.py": "",
		"svc/a/tests/sub/test_nested.py":  "x = 1\n",
		"svc/b/tests/test_other.py":       "x = 1\n",
	})
	if err := os.Remove(filepath.Join(top, "svc/a/tests/test_gone.py")); err != nil {
		t.Fatal(err)
	}
	want := []string{"tests/conftest.py", "tests/test_edit.py", "tests/test_gone.py", "tests/test_new.py"}
	for _, rootRel := range []string{".", "svc", "svc/a"} {
		root := filepath.Join(top, filepath.FromSlash(rootRel))
		prefix := strings.TrimPrefix(relSlash(root, filepath.Join(top, "svc", "a")), ".")
		var expect []string
		for _, w := range want {
			expect = append(expect, strings.TrimPrefix(prefix+"/"+w, "/"))
		}
		got, err := changedTestFiles(runContext{root: root, top: top, ref: "HEAD"}, filepath.Join(top, "svc", "a"))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, expect) {
			t.Fatalf("root %s: changedTestFiles = %q, want %q; the unit's changed test-role files relative to the manifest root, deleted and untracked ones included (deleted ones are removed from the base tree), source files, ignored files, a nested unit's and another unit's files excluded", rootRel, got, expect)
		}
	}
}

var fobParamGen = rapid.SampledFrom([]string{"1", "a.b", "c::d", "x[1]", "]", "[", "a]b[c", "é", "1-2", ""})

// TestBaseNameStripsParameters: pytest names an instance test[<id>], and
// the id may hold any text, including "::", "[" and "]". The case folds on
// the name before the first "[", or one test with a "::" in its parameter
// id becomes several cases and none matches the selected test.
func TestBaseNameStripsParameters(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := strings.Join(append(rapid.SliceOfN(rapid.SampledFrom([]string{"TestK", "TestÉ"}), 0, 2).Draw(t, "classes"), rapid.SampledFrom(fobNames).Draw(t, "name")), "::")
		full := name
		if rapid.Bool().Draw(t, "param") {
			full += "[" + fobParamGen.Draw(t, "p1") + fobParamGen.Draw(t, "p2") + "]"
		}
		if got := baseName(full); got != name {
			t.Fatalf("baseName(%q) = %q, want %q", full, got, name)
		}
	})
}

type instanceOutcome int

const (
	instPass instanceOutcome = iota
	instFail
	instError
	instSkip
)

// foldModel is D9's rule over instance outcomes, and the bodies of the
// instances that decide it.
func foldModel(outcomes []instanceOutcome, bodies []string) (string, []string) {
	pick := func(o instanceOutcome) []string {
		var out []string
		for i, x := range outcomes {
			if x == o {
				out = append(out, bodies[i])
			}
		}
		return out
	}
	switch {
	case slices.Contains(outcomes, instFail):
		return "failed", pick(instFail)
	case slices.Contains(outcomes, instError):
		return "errored", pick(instError)
	case !slices.ContainsFunc(outcomes, func(o instanceOutcome) bool { return o != instSkip }):
		return "skipped", nil
	}
	return "passed", nil
}

func instanceCase(i int, o instanceOutcome, body string) adapterkit.XMLCase {
	c := adapterkit.XMLCase{Name: fmt.Sprintf("test_x[%d]", i), Classname: "tests/test_x.py"}
	switch o {
	case instFail:
		c.Failure = &adapterkit.XMLText{Message: "m", Body: body}
	case instError:
		c.Error = &adapterkit.XMLText{Message: "m", Body: body}
	case instSkip:
		c.Skipped = &struct{}{}
	}
	return c
}

func caseText(c adapterkit.XMLCase) string {
	if c.Failure != nil {
		return c.Failure.Body
	}
	if c.Error != nil {
		return c.Error.Body
	}
	return ""
}

// TestFoldCaseMatchesModel: D9's rule, a failure if any instance failed,
// else an error if any errored, else skipped if all skipped, else passed.
// One failing instance must fail the case ("Failing parameter instance
// fails the case").
func TestFoldCaseMatchesModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		outcomes := rapid.SliceOfN(rapid.SampledFrom([]instanceOutcome{instPass, instFail, instError, instSkip}), 1, 5).Draw(t, "outcomes")
		var in []adapterkit.XMLCase
		var bodies []string
		for i, o := range outcomes {
			bodies = append(bodies, fmt.Sprintf("BODY-%d", i))
			in = append(in, instanceCase(i, o, bodies[i]))
		}
		got := foldCase("TestK::test_x", "tests/test_x.py", in)
		if got.Name != "TestK::test_x" || got.Classname != "tests/test_x.py" {
			t.Fatalf("folded case %q in %q", got.Name, got.Classname)
		}
		want, deciding := foldModel(outcomes, bodies)
		if g := outcomeOf(got); g != want {
			t.Fatalf("instances %v folded to %s, want %s", outcomes, g, want)
		}
		for _, b := range deciding {
			if text := caseText(got); !strings.Contains(text, b) {
				t.Fatalf("folded text %q lacks %s; each deciding instance's output shows which parameter broke", text, b)
			}
		}
	})
}

func outcomeOf(c adapterkit.XMLCase) string {
	n := 0
	out := "passed"
	if c.Failure != nil {
		n, out = n+1, "failed"
	}
	if c.Error != nil {
		n, out = n+1, "errored"
	}
	if c.Skipped != nil {
		n, out = n+1, "skipped"
	}
	if n > 1 {
		return "ambiguous"
	}
	return out
}

type baseReport struct {
	selected    []selectedTest
	report      []adapterkit.XMLCase
	want        map[string]string
	collectBody map[string]string
}

// drawRanTest adds a test's parameter instances to the report and returns
// its folded outcome.
func (r *baseReport) drawRanTest(t *rapid.T, f, name string) string {
	outcome := "passed"
	for i := range rapid.IntRange(1, 3).Draw(t, "instances") {
		c := adapterkit.XMLCase{Name: fmt.Sprintf("%s[%d]", name, i), Classname: f}
		if rapid.Bool().Draw(t, "fails") {
			c.Failure, outcome = &adapterkit.XMLText{Body: "boom"}, "failed"
		}
		r.report = append(r.report, c)
	}
	return outcome
}

func (r *baseReport) drawFile(t *rapid.T, f string) {
	if rapid.Bool().Draw(t, "collectError") {
		r.collectBody[f] = "E   ModuleNotFoundError: " + f
		r.report = append(r.report, adapterkit.XMLCase{Name: collectionCase, Classname: f, Error: &adapterkit.XMLText{Message: "collection failure", Body: r.collectBody[f]}})
	}
	seen := map[string]bool{}
	for range rapid.IntRange(0, 3).Draw(t, "tests") {
		name := strings.TrimPrefix(rapid.SampledFrom([]string{"", "TestK"}).Draw(t, "class")+"::"+rapid.SampledFrom(fobNames).Draw(t, "name"), "::")
		if seen[name] {
			continue
		}
		seen[name] = true
		sel := rapid.Bool().Draw(t, "selected")
		if sel {
			r.selected = append(r.selected, selectedTest{file: f, id: f + "::" + name})
		}
		outcome := "errored"
		if rapid.SampledFrom([]string{"ran", "ran", "missing"}).Draw(t, "state") == "ran" && r.collectBody[f] == "" {
			outcome = r.drawRanTest(t, f, name)
		}
		if sel {
			r.want[f+"."+name] = outcome
		}
	}
}

func (r *baseReport) check(t *rapid.T, suites []adapterkit.XMLSuite) {
	got := map[string]string{}
	for _, s := range suites {
		if s.Tests != len(s.Cases) {
			t.Fatalf("suite %s tests=%d with %d cases", s.Name, s.Tests, len(s.Cases))
		}
		for _, c := range s.Cases {
			key := c.Classname + "." + c.Name
			if _, dup := got[key]; dup {
				t.Fatalf("two cases for %s", key)
			}
			got[key] = outcomeOf(c)
			r.checkErrorText(t, key, c)
		}
	}
	if !maps.Equal(got, r.want) {
		t.Fatalf("cases %v\nwant  %v\none case per selected test (parameters folded), none for unselected tests", got, r.want)
	}
}

func (r *baseReport) checkErrorText(t *rapid.T, key string, c adapterkit.XMLCase) {
	if c.Error == nil {
		return
	}
	if b := r.collectBody[c.Classname]; b != "" && !strings.Contains(c.Error.Body, b) {
		t.Fatalf("%s error %q lacks the collection output %q; the spec wants the tool's output for a test that cannot be collected on base", key, c.Error.Body, b)
	}
	if r.collectBody[c.Classname] == "" && !strings.Contains(c.Error.Body, "PYTEST OUTPUT") {
		t.Fatalf("%s error %q lacks pytest's output; the developer needs it to see why the test did not run", key, c.Error.Body)
	}
}

// TestBaseCasesOnePerSelectedTest: the spec wants one case per selected
// test and no other cases; a selected test whose file failed to collect on
// base errors with that collection output, and one absent from the report
// errors with pytest's output rather than vanishing.
func TestBaseCasesOnePerSelectedTest(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := &baseReport{want: map[string]string{}, collectBody: map[string]string{}}
		for _, f := range []string{"tests/test_a.py", "tests/test_b.py"} {
			r.drawFile(t, f)
		}
		r.check(t, baseCases(r.selected, []adapterkit.XMLSuite{{Name: "x", Cases: rapid.Permutation(r.report).Draw(t, "order")}}, []byte("PYTEST OUTPUT")))
	})
}
