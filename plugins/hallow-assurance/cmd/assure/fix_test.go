package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

const waiversExceptM3b1 = `- {objective: IND-VERIFIER-DISTINCT, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: CFG-PROTECTED, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: VER-TEST-BUDGET, scope: '**', rationale: test budget check arrives in M3b-2, approver: owner, expires: 2099-01-01}
- {objective: VER-ROBUST-FUZZ, scope: '**', rationale: fuzz evidence arrives in M3b-2, approver: owner, expires: 2099-01-01}
`

const lastSrc = "package p\n\nfunc Last(xs []int) int {\n\treturn xs[len(xs)-2]\n}\n"

func fixRepo(t *testing.T, tools ...string) repo {
	t.Helper()
	path := toolPath(t, append([]string{"go", "golangci-lint"}, tools...)...)
	r := newRepo(t, levelB)
	t.Setenv("PATH", path)
	r.write(".gitignore", ".assure/state/\n")
	r.write("go.mod", "module example.com/m\n\ngo 1.26\n")
	r.write("p/p.go", lastSrc)
	r.write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestLen(t *testing.T) {\n\tif Last([]int{1, 2, 3}) == 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	r.write(".assure/waivers.yaml", waiversExceptM3b1)
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	return r
}

func commitWith(r repo, msg ...string) {
	r.git("add", "-A")
	args := []string{"commit", "-q"}
	for _, m := range msg {
		args = append(args, "-m", m)
	}
	r.git(args...)
}

func TestFixCommitReadsOnlyTrailers(t *testing.T) {
	for _, c := range []struct {
		name string
		msg  []string
		want bool
	}{
		{"trailer", []string{"fix last", "Assure-Kind: fix"}, true},
		{"lowercase key", []string{"fix last", "assure-kind: fix"}, true},
		{"mid-body text", []string{"fix last", "Assure-Kind: fix", "more words after the paragraph"}, false},
		{"no trailer", []string{"feature"}, false},
		{"other value", []string{"feature", "Assure-Kind: feature"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := fixRepo(t)
			r.write("p/q.go", "package p\n")
			commitWith(r, c.msg...)
			got, err := app.FixCommit(r.root, "HEAD~1")
			if err != nil || got != c.want {
				t.Fatalf("FixCommit = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestFixCommitIgnoresUncommittedWork(t *testing.T) {
	r := fixRepo(t)
	r.write("p/q.go", "package p\n")
	if got, err := app.FixCommit(r.root, "HEAD"); err != nil || got {
		t.Fatalf("FixCommit = %v, %v; uncommitted work carries no trailer", got, err)
	}
}

func TestEvaluateWithoutFixTrailerSkipsFailOnBase(t *testing.T) {
	r := fixRepo(t)
	r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
	commitWith(r, "feature")
	assure("evaluate", "--changed-from", "HEAD~1")
	rep, _ := readReport(t, r)
	if e, ok := entry(rep, "VER-FAIL-ON-BASE", "go"); ok {
		t.Fatalf("VER-FAIL-ON-BASE in report without a fix trailer: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(r.root, app.EvidenceDir, "evaluate", "go", "VER-FAIL-ON-BASE")); err == nil {
		t.Fatal("the fail-on-base adapter ran for a change that is not a fix")
	}
}

func TestEvaluateFixWithRegressionTestPasses(t *testing.T) {
	r := fixRepo(t)
	r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
	r.write("p/last_test.go", "package p\n\nimport \"testing\"\n\nfunc TestLastIsLast(t *testing.T) {\n\tif Last([]int{1, 2, 3}) != 3 {\n\t\tt.Fatal(\"off by one\")\n\t}\n}\n")
	commitWith(r, "fix off-by-one in Last", "Assure-Kind: fix")
	_, stdout, stderr := assure("evaluate", "--changed-from", "HEAD~1")
	rep, _ := readReport(t, r)
	if e, ok := entry(rep, "VER-FAIL-ON-BASE", "go"); !ok || e.Status != "pass" {
		t.Fatalf("VER-FAIL-ON-BASE: %+v %v\n%s\n%s", e, ok, stdout, stderr)
	}
}

func TestEvaluateFixWhoseTestPassesOnBaseFails(t *testing.T) {
	r := fixRepo(t)
	r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
	r.write("p/last_test.go", "package p\n\nimport \"testing\"\n\nfunc TestLastNonZero(t *testing.T) {\n\tif Last([]int{1, 2, 3}) == 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	commitWith(r, "fix off-by-one in Last", "Assure-Kind: fix")
	code, stdout, _ := assure("evaluate", "--changed-from", "HEAD~1")
	rep, _ := readReport(t, r)
	e, _ := entry(rep, "VER-FAIL-ON-BASE", "go")
	if code != 1 || e.Status != "fail" || !strings.Contains(strings.Join(e.Details, "\n"), "passes on base: p.TestLastNonZero") {
		t.Fatalf("exit %d, VER-FAIL-ON-BASE %+v; a test that passes before the fix does not show the bug", code, e)
	}
	if !strings.Contains(stdout, "passes on base") {
		t.Fatalf("summary lacks the detail:\n%s", stdout)
	}
}

func TestEvaluateWeaklyTestedChangeFailsMutation(t *testing.T) {
	r := fixRepo(t, "gremlins")
	r.write("p/p.go", lastSrc+"\nfunc Pos(n int) bool {\n\treturn n > 0\n}\n")
	r.write("p/pos_test.go", "package p\n\nimport \"testing\"\n\nfunc TestPos(t *testing.T) {\n\tif !Pos(5) {\n\t\tt.Fatal(\"pos\")\n\t}\n}\n")
	code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD")
	rep, _ := readReport(t, r)
	e, _ := entry(rep, "VER-MUTATION-CHANGED", "go")
	d := strings.Join(e.Details, "\n")
	if code != 1 || e.Status != "fail" || !strings.Contains(d, "p/p.go:8") || !strings.Contains(d, "threshold 65") {
		t.Fatalf("exit %d, VER-MUTATION-CHANGED %+v\n%s\n%s", code, e, stdout, stderr)
	}
	if rep.ToolVersions["go"]["gremlins"] == "" {
		t.Fatalf("tool_versions %v lacks gremlins", rep.ToolVersions)
	}
}
