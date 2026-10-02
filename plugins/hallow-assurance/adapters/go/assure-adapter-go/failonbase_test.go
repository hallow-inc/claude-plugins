package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

type genFunc struct {
	name, param, body, doc string
}

func (f genFunc) src() string {
	s := ""
	if f.doc != "" {
		s += "// " + f.doc + "\n"
	}
	return s + fmt.Sprintf("func %s(x %s) {\n\t_ = %q\n}\n", f.name, f.param, f.body)
}

func fileOf(fs []genFunc) []byte {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"testing\"\n\nvar _ testing.TB\n\n")
	for _, f := range fs {
		b.WriteString(f.src() + "\n")
	}
	return []byte(b.String())
}

func isGoTest(f genFunc) bool {
	return f.param == "*testing.T" && isTestName(f.name, "Test") || f.param == "*testing.F" && isTestName(f.name, "Fuzz")
}

func TestSelectionIsNewOrChangedTestFuncs(t *testing.T) {
	names := []string{"TestA", "TestB", "Testb", "FuzzC", "Test", "helper", "TestÜber"}
	params := []string{"*testing.T", "*testing.F", "int"}
	rapid.Check(t, func(t *rapid.T) {
		var base, head []genFunc
		want := []string{}
		for _, n := range names {
			p := rapid.SampledFrom(params).Draw(t, "param "+n)
			inBase, inHead := rapid.Bool().Draw(t, "base "+n), rapid.Bool().Draw(t, "head "+n)
			bf := genFunc{name: n, param: p, body: rapid.SampledFrom([]string{"x", "y"}).Draw(t, "bbody "+n), doc: rapid.SampledFrom([]string{"", "old"}).Draw(t, "bdoc "+n)}
			hf := genFunc{name: n, param: p, body: rapid.SampledFrom([]string{"x", "y"}).Draw(t, "hbody "+n), doc: rapid.SampledFrom([]string{"", "new"}).Draw(t, "hdoc "+n)}
			if inBase {
				base = append(base, bf)
			}
			if inHead {
				head = append(head, hf)
				if isGoTest(hf) && (!inBase || bf.body != hf.body) {
					want = append(want, n)
				}
			}
		}
		bfns, err := testFuncs(fileOf(base))
		if err != nil {
			t.Fatal(err)
		}
		hfns, err := testFuncs(fileOf(head))
		if err != nil {
			t.Fatal(err)
		}
		got := changedFuncs(bfns, hfns)
		if got == nil {
			got = []string{}
		}
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Fatalf("selected %v, want %v\nbase:\n%s\nhead:\n%s", got, want, fileOf(base), fileOf(head))
		}
	})
}

func TestHelperChangeSelectsOnlyCallersWhoseTextChanged(t *testing.T) {
	base := []byte("package p\n\nimport \"testing\"\n\nfunc helper() int { return 1 }\n\nfunc TestA(t *testing.T) { _ = helper() }\n")
	head := []byte(strings.ReplaceAll(string(base), "helper()", "helper2()"))
	head = []byte(strings.Replace(string(head), "func helper2() int", "func helper2() int", 1))
	bf, _ := testFuncs(base)
	hf, _ := testFuncs(head)
	if got := changedFuncs(bf, hf); !slices.Equal(got, []string{"TestA"}) {
		t.Fatalf("got %v; TestA's body changed because it calls the renamed helper", got)
	}
	onlyHelper := []byte(strings.Replace(string(base), "func helper() int { return 1 }", "func helper() int { return 2 }", 1))
	of, _ := testFuncs(onlyHelper)
	if got := changedFuncs(bf, of); len(got) != 0 {
		t.Fatalf("got %v; changing a non-test helper selects no test", got)
	}
}

func baseRun(t *testing.T, dir string) (ev.JUnit, runResponse) {
	t.Helper()
	out := t.TempDir()
	resp, stderr, code := runIn(t, dir, "run", "VER-FAIL-ON-BASE", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("run failed %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	return j, resp
}

const offByOne = "package a\n\nfunc Last(xs []int) int {\n\treturn xs[len(xs)-2]\n}\n"
const oldTest = "package a\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) {\n\tif Last([]int{1, 2, 3}) == 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n"

func gitState(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	for _, args := range [][]string{{"status", "--porcelain"}, {"worktree", "list", "--porcelain"}, {"ls-files", "--stage"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		b.Write(out)
	}
	return b.String()
}

func TestFailOnBaseRegressionTestFails(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": offByOne, "a/a_test.go": oldTest})
	writeFiles(t, dir, map[string]string{
		"a/a.go":      strings.Replace(offByOne, "len(xs)-2", "len(xs)-1", 1),
		"a/a_test.go": oldTest + "\nfunc TestBoundary(t *testing.T) {\n\tif Last([]int{1, 2, 3}) != 3 {\n\t\tt.Fatal(\"off by one\")\n\t}\n\tt.Run(\"sub\", func(t *testing.T) {})\n}\n",
	})
	before := gitState(t, dir)
	j, resp := baseRun(t, dir)
	if j.Cases != 1 || j.Failures != 1 || !strings.HasSuffix(j.Failing[0].Name, "TestBoundary") {
		t.Fatalf("got %+v; only the new top-level TestBoundary runs, and it must fail on the old code", j)
	}
	if resp.ToolVersions["go"] == "" {
		t.Fatalf("tool_versions %v lacks go", resp.ToolVersions)
	}
	if after := gitState(t, dir); after != before {
		t.Fatalf("repository changed:\nbefore %s\nafter %s", before, after)
	}
}

func TestFailOnBaseNewAPIIsAnError(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": offByOne, "a/a_test.go": oldTest})
	writeFiles(t, dir, map[string]string{
		"a/a.go":      offByOne + "\nfunc Last2(xs []int) int { return xs[len(xs)-1] }\n",
		"a/b_test.go": "package a\n\nimport \"testing\"\n\nfunc TestLast2(t *testing.T) {\n\tif Last2([]int{1}) != 1 {\n\t\tt.Fatal(\"x\")\n\t}\n}\n",
	})
	j, _ := baseRun(t, dir)
	if j.Cases != 1 || j.Errors != 1 || !strings.Contains(j.Failing[0].Text, "Last2") {
		t.Fatalf("got %+v; a test that needs the fix's new API errors on base with the compiler's message", j)
	}
}

func TestFailOnBaseDeletedTestFileIsRemoved(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": offByOne, "a/a_test.go": oldTest, "a/dup_test.go": "package a\n\nimport \"testing\"\n\nfunc TestDup(t *testing.T) {}\n"})
	if err := os.Remove(filepath.Join(dir, "a/dup_test.go")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"a/c_test.go": "package a\n\nimport \"testing\"\n\nfunc TestDup(t *testing.T) { t.Fatal(\"moved\") }\n"})
	j, _ := baseRun(t, dir)
	if j.Cases != 1 || j.Failures != 1 {
		t.Fatalf("got %+v; the deleted file's TestDup must be gone on base, or the package would not compile", j)
	}
}

func TestFailOnBaseUnchangedTestsNotSelected(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": offByOne, "a/a_test.go": oldTest})
	writeFiles(t, dir, map[string]string{"a/a_test.go": oldTest + "\n// comment only\n"})
	if j, _ := baseRun(t, dir); j.Cases != 0 {
		t.Fatalf("got %+v; TestOld is unchanged and must not be selected", j)
	}
}

func TestFailOnBaseModuleBelowGitRoot(t *testing.T) {
	top := t.TempDir()
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	writeFiles(t, top, map[string]string{"mod/go.mod": "module example.com/m\n\ngo 1.26\n", "mod/a/a.go": offByOne, "mod/a/a_test.go": oldTest})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-qm", "init")
	dir := filepath.Join(top, "mod")
	writeFiles(t, dir, map[string]string{
		"a/a.go":      strings.Replace(offByOne, "len(xs)-2", "len(xs)-1", 1),
		"a/a_test.go": oldTest + "\nfunc TestBoundary(t *testing.T) {\n\tif Last([]int{1, 2, 3}) != 3 {\n\t\tt.Fatal(\"off by one\")\n\t}\n}\n",
	})
	j, _ := baseRun(t, dir)
	if j.Cases != 1 || j.Failures != 1 || j.Errors != 0 || !strings.HasSuffix(j.Failing[0].Name, "TestBoundary") {
		t.Fatalf("got %+v; with go.mod below the git root the base tree must still be extracted, so the regression test fails on base instead of erroring", j)
	}
}
