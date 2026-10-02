package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func junitOf(t interface{ Fatalf(string, ...any) }, events []event) ev.JUnit {
	data, err := xml.Marshal(toJUnit(events))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	return j
}

func TestConversionPreservesFailures(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var events []event
		want := 0
		for p := range rapid.IntRange(1, 3).Draw(t, "npkgs") {
			pkg := fmt.Sprintf("example.com/m/p%d", p)
			anyFailed := false
			for i := range rapid.IntRange(0, 3).Draw(t, "ntests") {
				name := fmt.Sprintf("Test%d", i)
				if rapid.Bool().Draw(t, "subtest") {
					name += "/case"
				}
				events = append(events, event{Action: "run", Package: pkg, Test: name},
					event{Action: "output", Package: pkg, Test: name, Output: "=== RUN " + name + "\n"})
				switch rapid.SampledFrom([]string{"pass", "fail", "skip", "none"}).Draw(t, "final") {
				case "fail":
					anyFailed = true
					want++
					events = append(events, event{Action: "fail", Package: pkg, Test: name})
				case "pass":
					events = append(events, event{Action: "pass", Package: pkg, Test: name})
				case "skip":
					events = append(events, event{Action: "skip", Package: pkg, Test: name})
				}
			}
			switch rapid.SampledFrom([]string{"pass", "fail", "build", "none"}).Draw(t, "pkgFinal") {
			case "fail":
				if !anyFailed {
					want++
				}
				events = append(events, event{Action: "fail", Package: pkg})
			case "build":
				ip := pkg + " [" + pkg + ".test]"
				events = append(events, event{Action: "build-output", ImportPath: ip, Output: "x.go:1:1: bad\n"},
					event{Action: "build-fail", ImportPath: ip},
					event{Action: "fail", Package: pkg, FailedBuild: ip})
				if !anyFailed {
					want++
				}
			case "pass":
				events = append(events, event{Action: "pass", Package: pkg})
			}
		}
		if rapid.Bool().Draw(t, "orphanBuildFail") {
			events = append(events, event{Action: "build-output", ImportPath: "./missing", Output: "no dir\n"},
				event{Action: "build-fail", ImportPath: "./missing"})
			want++
		}
		j := junitOf(t, events)
		if got := j.Failures + j.Errors; got != want {
			t.Fatalf("failures+errors = %d, want %d", got, want)
		}
	})
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func committedModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.26\n"})
	writeFiles(t, dir, files)
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

func selection(t *testing.T, dir string) map[string][]string {
	t.Helper()
	paths, err := changedPaths(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return selectPackages(dir, paths)
}

func TestChangedPackageSelection(t *testing.T) {
	base := map[string]string{
		"a/a.go": "package a\n", "b/b.go": "package b\n",
		"c/d/one.go": "package d\n", "c/d/two.go": "package d\n",
		"gone/only.go": "package gone\n", "README.md": "x\n",
	}
	t.Run("edited, untracked, and surviving-package delete", func(t *testing.T) {
		dir := committedModule(t, base)
		writeFiles(t, dir, map[string]string{"a/a.go": "package a\n\nvar X = 1\n", "b/new.go": "package b\n"})
		git(t, dir, "rm", "-q", "c/d/two.go", "gone/only.go")
		want := map[string][]string{".": {"./a", "./b", "./c/d"}}
		if got := selection(t, dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("go.mod selects the module", func(t *testing.T) {
		dir := committedModule(t, base)
		writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.26.0\n", "a/a.go": "package a\n\nvar Y = 2\n"})
		want := map[string][]string{".": {"./..."}}
		if got := selection(t, dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("non-Go change selects nothing", func(t *testing.T) {
		dir := committedModule(t, base)
		writeFiles(t, dir, map[string]string{"README.md": "y\n", "a/testdata/x.go": "package x\n"})
		if got := selection(t, dir); len(got) != 0 {
			t.Fatalf("got %v, want nothing", got)
		}
	})
}

func runIn(t *testing.T, dir string, args ...string) (runResponse, string, int) {
	t.Helper()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	if code != 0 {
		return runResponse{}, errb.String(), code
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, out.Bytes()); err != nil || len(vs) > 0 {
		t.Fatalf("run response invalid: %v %v\n%s", err, vs, out.Bytes())
	}
	var resp runResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp, errb.String(), code
}

const failingTest = "package a\n\nimport \"testing\"\n\nfunc TestNo(t *testing.T) { t.Fatal(\"no\") }\n"

func TestRunTestsFailingTestStillExitsZero(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"a/a_test.go": failingTest, "bad/bad.go": "package bad\n\nfunc F() int { return \"x\" }\n"})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(resp.Evidence) != 1 || resp.Evidence[0].Type != "test.junit" || resp.ToolVersions["go"] == "" {
		t.Fatalf("response %+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	if j.Failures != 1 || j.Errors != 1 {
		t.Fatalf("want one failing test and one build error, got %+v", j)
	}
}

func TestRunLintReportsVetFindingAtRepoPath(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n"})
	writeFiles(t, dir, map[string]string{"x/a.go": "package x\n\nimport \"fmt\"\n\nfunc F() { fmt.Printf(\"%d\\n\", \"s\") }\n"})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "CODE-ZERO-WARNINGS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if resp.ToolVersions["golangci-lint"] == "" {
		t.Fatalf("missing golangci-lint version: %+v", resp.ToolVersions)
	}
	data, err := os.ReadFile(filepath.Join(out, "lint.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := ev.ParseSARIF(data)
	if err != nil {
		t.Fatal(err)
	}
	vet := false
	for _, r := range results {
		if r.URI != "x/a.go" {
			t.Errorf("result %+v not at repo-relative x/a.go", r)
		}
		vet = vet || r.RuleID == "govet"
	}
	if !vet {
		t.Fatalf("no govet result in %+v", results)
	}
}

func TestRunNoChangedGoFilesWritesEmptyEvidence(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n", "README.md": "x\n"})
	writeFiles(t, dir, map[string]string{"README.md": "y\n"})
	out := filepath.Join(t.TempDir(), "ev")
	if _, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, _ := os.ReadFile(filepath.Join(out, "junit.xml"))
	if j, err := ev.ParseJUnit(data); err != nil || j.Cases != 0 {
		t.Fatalf("want empty JUnit, got %+v %v", j, err)
	}
}

func TestRunMissingLinterFails(t *testing.T) {
	bin := t.TempDir()
	for _, tool := range []string{"go", "git"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	t.Setenv("PATH", bin)
	_, stderr, code := runIn(t, dir, "run", "CODE-ZERO-WARNINGS", "--changed-from", "HEAD", "--out", t.TempDir())
	if code == 0 || !strings.Contains(stderr, "golangci-lint not found") {
		t.Fatalf("want failure naming golangci-lint, got %d %q", code, stderr)
	}
}

func TestRunUnknownObjective(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"run", "VER-NOT-AN-OBJECTIVE", "--changed-from", "HEAD", "--out", t.TempDir()}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "not implemented") {
		t.Fatalf("exit %d, stderr %q; an unimplemented objective must exit 2 naming it", code, errb.String())
	}
}

func lintSARIF(t *testing.T, dir, objective string) []ev.Result {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", objective, "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(resp.Evidence) != 1 || resp.Evidence[0].Type != "lint.sarif" {
		t.Fatalf("response %+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	results, err := ev.ParseSARIF(data)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func TestRunLintReportsOldFinding(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n\nfunc unusedHelper() {}\n"})
	writeFiles(t, dir, map[string]string{"x/b.go": "package x\n\nfunc G() {}\n"})
	results := lintSARIF(t, dir, "CODE-ZERO-WARNINGS")
	found := false
	for _, r := range results {
		found = found || (r.RuleID == "unused" && r.URI == "x/a.go")
	}
	if !found {
		t.Fatalf("old finding in x/a.go missing from %+v; the ratchet lives in the evaluator baseline, not the adapter", results)
	}
}

func needGolangci(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
}

const droppedErrors = "package x\n\nimport \"os\"\n\nfunc F() {\n\tos.Remove(\"a\")\n\tos.Remove(\"b\")\n\tos.Remove(\"c\")\n\tos.Remove(\"d\")\n}\n"

func TestRepoConfigCannotDisableCheckReturns(t *testing.T) {
	needGolangci(t)
	dir := committedModule(t, map[string]string{
		".golangci.yml": "version: \"2\"\nlinters:\n  default: none\n  enable: [unused]\n",
		"x/a.go":        "package x\n",
	})
	writeFiles(t, dir, map[string]string{"x/a.go": droppedErrors})
	n := 0
	for _, r := range lintSARIF(t, dir, "CODE-CHECK-RETURNS") {
		if r.RuleID == "errcheck" && r.URI == "x/a.go" {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("got %d errcheck results, want 4: the repo config must not weaken the pack, and same-text findings must not be capped (the baseline counts them)", n)
	}
}

func TestResourceBoundsReportsNoctxAtRepoPath(t *testing.T) {
	needGolangci(t)
	dir := committedModule(t, map[string]string{"sub/x/a.go": "package x\n"})
	writeFiles(t, dir, map[string]string{"sub/x/a.go": "package x\n\nimport \"net/http\"\n\nfunc F() (*http.Response, error) {\n\treturn http.Get(\"http://example.com\")\n}\n"})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "CODE-RESOURCE-BOUNDS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if resp.ToolVersions["golangci-lint"] == "" {
		t.Fatalf("tool_versions %v lacks golangci-lint", resp.ToolVersions)
	}
	data, err := os.ReadFile(filepath.Join(out, "lint.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := ev.ParseSARIF(data)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		if r.URI != "sub/x/a.go" {
			t.Errorf("result %+v not at repo-relative sub/x/a.go; the adapter-written config moves golangci-lint's path base", r)
		}
		found = found || r.RuleID == "noctx"
	}
	if !found {
		t.Fatalf("no noctx result in %+v", results)
	}
}

func TestComplexityIsRawMetricPerFunction(t *testing.T) {
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n"})
	rapid.Check(t, func(rt *rapid.T) {
		ks := rapid.SliceOfN(rapid.IntRange(0, 20), 1, 3).Draw(rt, "ifs")
		var src strings.Builder
		src.WriteString("package x\n")
		for i, k := range ks {
			fmt.Fprintf(&src, "\nfunc F%d(v int) int {\n", i)
			for j := range k {
				fmt.Fprintf(&src, "\tif v == %d {\n\t\treturn %d\n\t}\n", j, j)
			}
			src.WriteString("\treturn -1\n}\n")
		}
		writeFiles(t, dir, map[string]string{
			"x/a.go":      src.String(),
			"x/a_test.go": "package x\n\nfunc helper(v int) bool { return v > 0 && v < 9 }\n",
			"x/gen.go":    "// Code generated by hand. DO NOT EDIT.\n\npackage x\n\nfunc Gen(v int) bool { return v > 0 && v < 9 }\n",
		})
		out := t.TempDir()
		if _, err := runComplexity(dir, "", out, map[string][]string{".": {"./x"}}, nil); err != nil {
			rt.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(out, "lint.sarif"))
		if err != nil {
			rt.Fatal(err)
		}
		results, err := ev.ParseSARIF(data)
		if err != nil {
			rt.Fatal(err)
		}
		got := map[string]float64{}
		for _, r := range results {
			if r.URI != "x/a.go" || r.RuleID != "cyclomatic" || !r.HasMetric {
				rt.Fatalf("unexpected result %+v; test and generated files must emit nothing", r)
			}
			got[r.Message] = r.Metric
		}
		for i, k := range ks {
			name := fmt.Sprintf("cyclomatic complexity of x.F%d", i)
			if got[name] != float64(k+1) {
				rt.Fatalf("%s = %v, want %d; the evaluator applies the threshold to this number", name, got[name], k+1)
			}
		}
		if len(got) != len(ks) {
			rt.Fatalf("got %v, want one result per function", got)
		}
	})
}

func TestNoUnsafeScan(t *testing.T) {
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n"})
	writeFiles(t, dir, map[string]string{
		"x/a.go":      "package x\n\nimport (\n\t\"reflect\"\n\t\"unsafe\"\n)\n\nvar _ = unsafe.Sizeof(0)\nvar _ = reflect.TypeOf(0)\n",
		"x/b.go":      "package x\n\nimport _ \"unsafe\"\n\n//go:linkname now runtime.nanotime\nfunc now() int64\n",
		"x/a_test.go": "package x\n\nimport \"reflect\"\n\nvar _ = reflect.DeepEqual\n",
		"x/gen.go":    "// Code generated by hand. DO NOT EDIT.\n\npackage x\n\nimport \"unsafe\"\n\nvar _ = unsafe.Sizeof(1)\n",
	})
	var got []string
	for _, r := range lintSARIF(t, dir, "CODE-NO-UNSAFE") {
		got = append(got, r.URI+" "+r.RuleID)
	}
	want := []string{"x/a.go import-reflect", "x/a.go import-unsafe", "x/b.go import-unsafe", "x/b.go linkname"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v; test and generated files are exempt, everything else is reported", got, want)
	}
}

func TestNoUnsafeTestOnlyReflectIsClean(t *testing.T) {
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n"})
	writeFiles(t, dir, map[string]string{"x/a_test.go": "package x\n\nimport \"reflect\"\n\nvar _ = reflect.DeepEqual\n"})
	if rs := lintSARIF(t, dir, "CODE-NO-UNSAFE"); len(rs) != 0 {
		t.Fatalf("got %+v", rs)
	}
}

func TestRunComplexityEndToEnd(t *testing.T) {
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n"})
	writeFiles(t, dir, map[string]string{"x/a.go": "package x\n\nfunc F(v int) bool {\n\tif v > 1 {\n\t\treturn true\n\t}\n\treturn v < 0 || v == 0\n}\n"})
	rs := lintSARIF(t, dir, "CODE-COMPLEXITY")
	if len(rs) != 1 || rs[0].Metric != 3 || rs[0].URI != "x/a.go" {
		t.Fatalf("got %+v, want one cyclomatic result with metric 3", rs)
	}
}

func TestRunTestsRunsInShortMode(t *testing.T) {
	const onlyShort = "package a\n\nimport \"testing\"\n\nfunc TestSlow(t *testing.T) {\n\tif !testing.Short() {\n\t\tt.Fatal(\"not short\")\n\t}\n}\n"
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"a/a_test.go": onlyShort})
	out := filepath.Join(t.TempDir(), "ev")
	if _, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	if j.Cases == 0 || j.Failures != 0 || j.Errors != 0 {
		t.Fatalf("want TestSlow run and passing under -short, got %+v", j)
	}
}
