package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

var (
	venvOnce sync.Once
	venvDir  string
	venvPy   string
	venvErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, d := range []string{venvDir, uvProject} {
		if d != "" {
			_ = os.RemoveAll(d)
		}
	}
	os.Exit(code)
}

// oldestPython is the oldest interpreter the adapter supports. Tests that run
// real Python pin it rather than letting uv pick the newest: helper.py runs in
// the adopter's interpreter, and stdlib fixes in newer versions (3.14's
// tokenize.detect_encoding splitting on "\r") hide helper bugs that crash on
// older ones.
const oldestPython = "3.11"

// testPython is a real interpreter in a fresh uv virtualenv, built once per
// process. The helper runs in the project's own interpreter, so a stand-in
// would not prove that its ast line numbers and the text it slices agree.
func testPython(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not on PATH; the ast helper needs a real interpreter, which these tests build with `uv venv` at the oldest supported Python (" + oldestPython + ", for tomllib) so version-dependent helper bugs show on every machine")
	}
	venvOnce.Do(func() {
		venvDir, venvErr = os.MkdirTemp("", "assure-py-venv-*")
		if venvErr != nil {
			return
		}
		dir := filepath.Join(venvDir, ".venv")
		cmd := exec.CommandContext(context.Background(), "uv", "venv", "--no-project", "--quiet", "--python", oldestPython, dir)
		cmd.Dir = venvDir
		if out, err := cmd.CombinedOutput(); err != nil {
			venvErr = fmt.Errorf("uv venv --python %s (the oldest supported Python, pinned so version-dependent helper bugs reproduce): %w\n%s", oldestPython, err, out)
			return
		}
		venvPy = filepath.Join(dir, "bin", "python")
	})
	if venvErr != nil {
		t.Fatalf("could not build the test virtualenv: %v", venvErr)
	}
	return venvPy
}

const (
	ctxModule = iota
	ctxTestClass
	ctxOther
)

type pyModel struct {
	nl    string
	lines []string
	funcs []pyFunc
	tests []pyTest
}

func (m *pyModel) emit(s string) int {
	m.lines = append(m.lines, s)
	return len(m.lines)
}

func (m *pyModel) text(start, end int) string {
	var b strings.Builder
	for _, l := range m.lines[start-1 : end] {
		b.WriteString(l + m.nl)
	}
	return b.String()
}

var fillerGen = rapid.SampledFrom([]string{"", "   ", "# note é", "\f", "\f# page"})

func (m *pyModel) fillers(t *rapid.T, indent string) {
	for range rapid.IntRange(0, 2).Draw(t, "fillers") {
		f := fillerGen.Draw(t, "filler")
		if strings.HasPrefix(f, "#") {
			f = indent + f
		}
		m.emit(f)
	}
}

func (m *pyModel) decorators(t *rapid.T, indent string) int {
	first := 0
	for range rapid.IntRange(0, 2).Draw(t, "decorators") {
		var l int
		if rapid.Bool().Draw(t, "multiline") {
			l = m.emit(indent + "@dec(")
			m.emit(indent + "    1,")
			m.emit(indent + ")")
		} else {
			l = m.emit(indent + "@dec")
		}
		if first == 0 {
			first = l
		}
		m.fillers(t, indent)
	}
	return first
}

var funcNameGen = rapid.SampledFrom([]string{"test_a", "testb", "test", "test_é", "tst_c", "check_d", "helper", "_test_e", "Test_f"})

var classNameGen = rapid.SampledFrom([]string{"TestA", "Testb", "Test", "TestÉ", "Helper", "ATest", "testc"})

func (m *pyModel) function(t *rapid.T, indent, prefix, class string, ctx, depth int) {
	deco := m.decorators(t, indent)
	name := funcNameGen.Draw(t, "func")
	kw := "def "
	if rapid.Bool().Draw(t, "async") {
		kw = "async def "
	}
	lead := indent
	if indent == "" && rapid.Bool().Draw(t, "formfeed") {
		lead = "\f"
	}
	def := m.emit(lead + kw + name + "():")
	m.body(t, indent+"    ", prefix+name+".", depth)
	end := len(m.lines)
	m.funcs = append(m.funcs, pyFunc{Name: prefix + name, Start: def, End: end})
	if ctx != ctxOther && strings.HasPrefix(name, "test") {
		start := def
		if deco != 0 {
			start = deco
		}
		m.tests = append(m.tests, pyTest{Class: class, Name: name, Start: start, End: end, Text: m.text(start, end)})
	}
}

func (m *pyModel) body(t *rapid.T, indent, prefix string, depth int) {
	kinds := []string{"simple", "string", "paren"}
	if depth < 2 {
		kinds = append(kinds, "def", "class")
	}
	for i := range rapid.IntRange(1, 3).Draw(t, "stmts") {
		if i > 0 {
			m.fillers(t, indent)
		}
		switch rapid.SampledFrom(kinds).Draw(t, "stmt") {
		case "simple":
			m.emit(indent + rapid.SampledFrom([]string{"pass", "x = 1", "return 2", "s = 'é'"}).Draw(t, "simple"))
		case "string":
			m.emit(indent + `s = """`)
			m.emit("  text é")
			m.emit(`"""`)
		case "paren":
			m.emit(indent + "y = (1 +")
			m.emit(indent + "     2)")
		case "def":
			m.function(t, indent, prefix, "", ctxOther, depth+1)
		case "class":
			m.class(t, indent, prefix, ctxOther, depth+1)
		}
	}
}

func (m *pyModel) class(t *rapid.T, indent, prefix string, ctx, depth int) {
	m.decorators(t, indent)
	name := classNameGen.Draw(t, "class")
	m.emit(indent + "class " + name + ":")
	inner := ctxOther
	if ctx == ctxModule && strings.HasPrefix(name, "Test") {
		inner = ctxTestClass
	}
	kinds := []string{"method", "attr"}
	if depth < 2 {
		kinds = append(kinds, "class")
	}
	in := indent + "    "
	for i := range rapid.IntRange(1, 3).Draw(t, "members") {
		if i > 0 {
			m.fillers(t, in)
		}
		switch rapid.SampledFrom(kinds).Draw(t, "member") {
		case "method":
			m.function(t, in, prefix+name+".", name, inner, depth+1)
		case "attr":
			m.emit(in + "attr = 1")
		case "class":
			m.class(t, in, prefix+name+".", ctxOther, depth+1)
		}
	}
}

func genModule(t *rapid.T) (*pyModel, []byte) {
	m := &pyModel{nl: rapid.SampledFrom([]string{"\n", "\r\n", "\r"}).Draw(t, "newline")}
	enc := rapid.SampledFrom([]string{"utf-8", "utf-8-bom", "latin-1"}).Draw(t, "encoding")
	if enc == "latin-1" {
		m.emit("# -*- coding: latin-1 -*-")
	}
	m.fillers(t, "")
	for i := range rapid.IntRange(0, 4).Draw(t, "items") {
		if i > 0 {
			m.fillers(t, "")
		}
		switch rapid.SampledFrom([]string{"def", "class", "stmt"}).Draw(t, "item") {
		case "def":
			m.function(t, "", "", "", ctxModule, 0)
		case "class":
			m.class(t, "", "", ctxModule, 0)
		case "stmt":
			m.emit("import os")
		}
	}
	src := m.text(1, len(m.lines))
	if len(m.lines) == 0 {
		src = ""
	}
	switch enc {
	case "utf-8-bom":
		return m, append([]byte("\xef\xbb\xbf"), src...)
	case "latin-1":
		var b []byte
		for _, r := range src {
			b = append(b, byte(r))
		}
		return m, b
	}
	return m, []byte(src)
}

func byStart[T pyTest | pyFunc](start func(T) int, name func(T) string) func(a, b T) int {
	return func(a, b T) int { return cmp.Or(cmp.Compare(start(a), start(b)), strings.Compare(name(a), name(b))) }
}

var (
	testOrder = byStart(func(x pyTest) int { return x.Start }, func(x pyTest) string { return x.Class + "::" + x.Name })
	funcOrder = byStart(func(x pyFunc) int { return x.Start }, func(x pyFunc) string { return x.Name })
)

var brokenGen = rapid.SampledFrom([]string{"def broken(:\n", "class X\n", "x = (\n", "@dec\n", "x = '\xff'\n", "def test_n():\n    pass\n\x00\n", "# coding: bogus\nx = 1\n"})

// TestDescribeFilesMatchesModel generates modules from a grammar whose
// tests and functions are known by construction. Fail-on-base selects a
// test when its text differs from base, and coverage reports FN from these
// spans, so a wrong line or a missed decorator either reruns unchanged tests
// or hides an edited one.
func TestDescribeFilesMatchesModel(t *testing.T) {
	py := testPython(t)
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp("", "assure-py-describe-*")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		m, src := genModule(t)
		const rel, broken = "tests/test_mod.py", "tests/test_broken.py"
		writeTree(t, dir, map[string]string{rel: string(src), broken: brokenGen.Draw(t, "broken")})
		files, err := describeFiles(py, dir, []string{rel, broken})
		if err != nil {
			t.Fatalf("describeFiles: %v\nsource %q\none bad file must give that file an error, not fail the whole batch", err, src)
		}
		if b := files[broken]; b.Error == "" || len(b.Tests) != 0 || len(b.Functions) != 0 {
			t.Fatalf("%s: got %+v; a file that does not parse must report an error and no tests, so no objective treats it as test-free", broken, b)
		}
		got := files[rel]
		if got.Error != "" {
			t.Fatalf("valid module reported error %q\nsource %q", got.Error, src)
		}
		slices.SortFunc(got.Tests, testOrder)
		slices.SortFunc(got.Functions, funcOrder)
		slices.SortFunc(m.tests, testOrder)
		slices.SortFunc(m.funcs, funcOrder)
		if !slices.Equal(got.Tests, m.tests) {
			t.Fatalf("tests\ngot  %+v\nwant %+v\nsource %q\npytest's default discovery collects module-level test* functions and test* methods of module-level Test* classes; the span starts at the first decorator and the text is exactly those source lines, so an edited decorator selects the test and class-level code does not", got.Tests, m.tests, src)
		}
		if !slices.Equal(got.Functions, m.funcs) {
			t.Fatalf("functions\ngot  %+v\nwant %+v\nsource %q\ncoverage FN entries need every function's qualified name, its def line (decorators excluded), and end line", got.Functions, m.funcs, src)
		}
		for _, tc := range got.Tests {
			want := rel + "::" + tc.Name
			if tc.Class != "" {
				want = rel + "::" + tc.Class + "::" + tc.Name
			}
			if id := tc.id(rel); id != want {
				t.Fatalf("id = %q, want %q; identity is the pytest node id without parameters", id, want)
			}
		}
	})
}

func TestSpecMethodInTestClass(t *testing.T) {
	py := testPython(t)
	dir := t.TempDir()
	const rel = "tests/test_range.py"
	writeTree(t, dir, map[string]string{rel: "class TestRange:\n    def test_includes_end(self):\n        assert True\n"})
	files, err := describeFiles(py, dir, []string{rel})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tc := range files[rel].Tests {
		ids = append(ids, tc.id(rel))
	}
	if want := []string{"tests/test_range.py::TestRange::test_includes_end"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %q, want %q (spec scenario \"Method in a test class\"); fail-on-base passes these ids to pytest, so any other form selects nothing", ids, want)
	}
}

func TestIdentityWithoutClass(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rel := pathGen.Draw(t, "rel")
		name := funcNameGen.Draw(t, "name")
		if got, want := (pyTest{Name: name}).id(rel), rel+"::"+name; got != want {
			t.Fatalf("id = %q, want %q; a module-level test's node id has no class segment", got, want)
		}
	})
}

// TestHelperIsIsolated plants modules that shadow the helper's stdlib
// imports in the working directory and on PYTHONPATH. The helper runs inside
// the adopter's repository and virtualenv, so it must not import their code.
func TestHelperIsIsolated(t *testing.T) {
	py := testPython(t)
	dir, shadow := t.TempDir(), t.TempDir()
	poison := "raise SystemExit('shadowed stdlib imported')\n"
	for _, d := range []string{dir, shadow} {
		writeTree(t, d, map[string]string{"json.py": poison, "ast.py": poison, "tokenize.py": poison})
	}
	writeTree(t, dir, map[string]string{"test_x.py": "def test_x():\n    pass\n"})
	t.Setenv("PYTHONPATH", shadow)
	files, err := describeFiles(py, dir, []string{"test_x.py"})
	if err != nil {
		t.Fatalf("describeFiles: %v; the helper must run in isolated mode (-I) so the repository's json.py or PYTHONPATH cannot replace its stdlib", err)
	}
	if n := len(files["test_x.py"].Tests); n != 1 {
		t.Fatalf("got %d tests, want 1", n)
	}
}

func fakePython(t *testing.T, script string) string {
	p := filepath.Join(t.TempDir(), "python")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestHelperFailuresFailClosed uses stand-in interpreters for the failures a
// real one rarely produces. Every objective that relies on test identity
// trusts this output, so a crash, garbage, or a missing file must be an
// error rather than "this file has no tests".
func TestHelperFailuresFailClosed(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"pyproject.toml": "", "test_x.py": ""})
	cases := map[string]struct{ script, want string }{
		"crash":   {"echo 'Traceback: boom' >&2; exit 3", "boom"},
		"garbage": {"echo not json", "helper"},
		"missing": {"echo '{}'", "test_x.py"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			py := fakePython(t, c.script)
			if _, err := describeFiles(py, dir, []string{"test_x.py"}); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("describeFiles err = %v, want an error mentioning %q", err, c.want)
			}
			if c.want == "test_x.py" {
				c.want = "pyproject.toml"
			}
			if _, err := discoveryRefusal(py, dir, dir); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("discoveryRefusal err = %v, want an error mentioning %q", err, c.want)
			}
		})
	}
}

func TestNothingToReadNeedsNoInterpreter(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-python")
	if files, err := describeFiles(missing, dir, nil); err != nil || len(files) != 0 {
		t.Fatalf("describeFiles with no files = %v, %v; want empty, nil", files, err)
	}
	if msg, err := discoveryRefusal(missing, dir, dir); err != nil || msg != "" {
		t.Fatalf("discoveryRefusal with no config files = %q, %v; want \"\", nil: a unit without pytest configuration uses default discovery", msg, err)
	}
}
