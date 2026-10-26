package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var discoveryKeys = []string{"python_files", "python_classes", "python_functions"}

var rightSections = map[string][]string{
	"pyproject.toml": {"[tool.pytest.ini_options]", "[tool.pytest]"},
	"pytest.ini":     {"[pytest]"},
	"tox.ini":        {"[pytest]"},
	"setup.cfg":      {"[tool:pytest]"},
}

var wrongSections = map[string][]string{
	"pyproject.toml": {"[tool.black]", "[project]", "[pytest]"},
	"pytest.ini":     {"[tool:pytest]", "[testenv]"},
	"tox.ini":        {"[tool:pytest]", "[testenv]"},
	"setup.cfg":      {"[pytest]", "[metadata]"},
}

func setting(file, key string, t *rapid.T) string {
	if file != "pyproject.toml" {
		return key + " = check_*\n"
	}
	if rapid.Bool().Draw(t, "list") {
		return key + ` = ["check_*"]` + "\n"
	}
	return key + ` = "check_*"` + "\n"
}

func testpaths(file string) string {
	if file == "pyproject.toml" {
		return "testpaths = [\"tests\"]\n"
	}
	return "testpaths = tests\n"
}

// drawConfig writes one config file and returns the discovery keys pytest
// would read from it.
func drawConfig(t *rapid.T, file string) (string, []string) {
	var keys []string
	for _, k := range discoveryKeys {
		if rapid.Bool().Draw(t, "key "+k) {
			keys = append(keys, k)
		}
	}
	var b strings.Builder
	if rapid.Bool().Draw(t, "wrong section") {
		b.WriteString(rapid.SampledFrom(wrongSections[file]).Draw(t, "wrong") + "\n")
		for _, k := range discoveryKeys {
			b.WriteString(setting(file, k, t))
		}
	}
	b.WriteString(rapid.SampledFrom(rightSections[file]).Draw(t, "right") + "\n")
	if rapid.Bool().Draw(t, "testpaths") {
		b.WriteString(testpaths(file))
	}
	for _, k := range keys {
		b.WriteString(setting(file, k, t))
	}
	return b.String(), keys
}

// linesFor returns the message lines line naming rel: the line whose longest mentioned
// candidate path is rel, so "pyproject.toml" is not mistaken for
// "a/pyproject.toml".
func linesFor(msg, rel string, candidates []string) []string {
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		best := ""
		for _, c := range candidates {
			if strings.Contains(line, c) && len(c) > len(best) {
				best = c
			}
		}
		if best == rel {
			out = append(out, line)
		}
	}
	return out
}

// TestDiscoveryRefusalMatchesModel draws pytest config files in the unit, its
// ancestors up to the git top-level, a child directory, and the directory
// above the top-level. Only discovery keys in the section pytest reads, in
// the unit or an ancestor within the repository, change which functions are
// tests, so only those may be refused, and each must name its file and keys.
func TestDiscoveryRefusalMatchesModel(t *testing.T) {
	py := testPython(t)
	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "assure-py-discovery-*")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(root) }()
		root, _ = filepath.EvalSymlinks(root)
		top := filepath.Join(root, "repo")
		unitRel := strings.Join([]string{"a", "b"}[:rapid.IntRange(0, 2).Draw(t, "unit depth")], "/")
		files, want, candidates := drawConfigTree(t, unitRel)
		writeTree(t, top, files)
		msg, err := discoveryRefusal(py, top, filepath.Join(top, filepath.FromSlash(unitRel)))
		if err != nil {
			t.Fatalf("discoveryRefusal: %v", err)
		}
		if len(want) == 0 && msg != "" {
			t.Fatalf("unit %q got refusal %q, want none\nfiles %q\nonly discovery keys in pytest's own section, in the unit or an ancestor inside the repository, change test identity; refusing anything else blocks projects that use default discovery", unitRel, msg, files)
		}
		for _, rel := range candidates {
			checkRefusal(t, msg, rel, want[rel], candidates)
		}
	})
}

func drawConfigTree(t *rapid.T, unitRel string) (map[string]string, map[string][]string, []string) {
	files := map[string]string{"pyproject.toml": "", filepath.ToSlash(filepath.Join(unitRel, "pyproject.toml")): ""}
	want := map[string][]string{}
	var candidates []string
	for _, d := range []string{"..", "", "a", "a/b", "a/b/c"} {
		for _, n := range pytestConfigFiles {
			if !rapid.Bool().Draw(t, "present "+d+"/"+n) {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(d, n))
			content, keys := drawConfig(t, n)
			files[rel] = content
			candidates = append(candidates, rel)
			inScope := d != ".." && (d == "" || d == unitRel || strings.HasPrefix(unitRel, d+"/"))
			if inScope && len(keys) > 0 {
				want[rel] = keys
			}
		}
	}
	return files, want, candidates
}

func checkRefusal(t *rapid.T, msg, rel string, keys, candidates []string) {
	lines := linesFor(msg, rel, candidates)
	if len(keys) == 0 {
		if len(lines) != 0 {
			t.Fatalf("%s is refused in %q but sets no discovery key pytest reads for this unit", rel, msg)
		}
		return
	}
	if len(lines) != 1 {
		t.Fatalf("%d lines name %s in %q, want 1; the environment message must name each file that sets custom discovery", len(lines), rel, msg)
	}
	for _, k := range discoveryKeys {
		if strings.Contains(lines[0], k) != slices.Contains(keys, k) {
			t.Fatalf("line %q: mentions %s = %v, want %v (keys set: %v); the message must name exactly the keys the file sets", lines[0], k, strings.Contains(lines[0], k), slices.Contains(keys, k), keys)
		}
	}
}

func TestSpecCustomDiscoveryRefused(t *testing.T) {
	py := testPython(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{"svc/pyproject.toml": "[tool.pytest.ini_options]\npython_functions = \"check_*\"\n"})
	msg, err := discoveryRefusal(py, top, filepath.Join(top, "svc"))
	if err != nil {
		t.Fatal(err)
	}
	mustMention(t, msg, "svc/pyproject.toml", "python_functions")
}

func TestTestpathsOnlyAccepted(t *testing.T) {
	py := testPython(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{
		"pyproject.toml": "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n",
		"pytest.ini":     "[pytest]\ntestpaths = tests\n",
		"tox.ini":        "[pytest]\ntestpaths = tests\n",
		"setup.cfg":      "[tool:pytest]\ntestpaths = tests\n",
	})
	if msg, err := discoveryRefusal(py, top, top); err != nil || msg != "" {
		t.Fatalf("got %q, %v; want no refusal: testpaths only narrows where pytest looks, not which functions are tests", msg, err)
	}
}

// TestMalformedConfigFailsClosed: a config pytest cannot parse might hold
// discovery keys, so the adapter must not assume default discovery.
func TestMalformedConfigFailsClosed(t *testing.T) {
	py := testPython(t)
	bad := map[string]string{
		"pyproject.toml": "[tool.pytest\npython_functions = \"check_*\"\n",
		"pytest.ini":     "python_functions = check_*\n",
		"tox.ini":        "python_functions = check_*\n[pytest\n",
		"setup.cfg":      "[tool:pytest]\npython_functions = \xff\n",
	}
	for _, n := range pytestConfigFiles {
		t.Run(n, func(t *testing.T) {
			top := realTempDir(t)
			writeTree(t, top, map[string]string{filepath.Join("svc", n): bad[n]})
			msg, err := discoveryRefusal(py, top, filepath.Join(top, "svc"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(msg, fmt.Sprintf("svc/%s", n)) {
				t.Fatalf("got %q; a malformed svc/%s must give an environment message naming it", msg, n)
			}
		})
	}
}
