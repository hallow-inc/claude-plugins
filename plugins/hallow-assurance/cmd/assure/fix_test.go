package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const waiversExceptM3b1 = `- {objective: IND-VERIFIER-DISTINCT, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: CFG-PROTECTED, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
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

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func gitIn(t fataler, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{
		"-c", "commit.gpgsign=false", "-c", "core.excludesFile=/dev/null", "-c", "user.email=t@t", "-c", "user.name=t",
	}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixSubject(t *rapid.T) string {
	typ := []byte("fix")
	for i := range typ {
		if rapid.Bool().Draw(t, "upper") {
			typ[i] -= 'a' - 'A'
		}
	}
	s := string(typ)
	if rapid.Bool().Draw(t, "scoped") {
		s += "(" + rapid.StringMatching(`[!-'*-~ ]{1,10}`).Draw(t, "scope") + ")"
	}
	if rapid.Bool().Draw(t, "breaking") {
		s += "!"
	}
	return s + ": " + rapid.StringMatching(`[a-z][a-z ]{0,15}[a-z]`).Draw(t, "description")
}

var nearMissSubjects = []string{"fixup! fix: x", "fixes: x", "fix:x", "fix(): x", "hotfix: x", "chore: fix x", "Merge branch 'fix'", "fix((a)): x", "fix(a)x: x", "fix !: x", "feat: x"}

func otherSubject(t *rapid.T) string {
	if rapid.Bool().Draw(t, "listed") {
		return rapid.SampledFrom(nearMissSubjects).Draw(t, "near miss")
	}
	typ := rapid.StringMatching(`[a-zA-Z]{1,8}`).Filter(func(s string) bool { return !strings.EqualFold(s, "fix") }).Draw(t, "type")
	return typ + ": " + rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "description")
}

func TestFixCommitIsAnySubjectTypedFix(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	rapid.Check(t, func(t *rapid.T) {
		from := gitIn(t, dir, "rev-parse", "HEAD")
		want := false
		var subjects []string
		for range rapid.IntRange(1, 3).Draw(t, "commits") {
			s := otherSubject(t)
			if rapid.Bool().Draw(t, "typed fix") {
				s, want = fixSubject(t), true
			}
			subjects = append(subjects, s)
			args := []string{"commit", "-q", "--allow-empty", "-m", s}
			if rapid.Bool().Draw(t, "trailer") {
				args = append(args, "-m", "Assure-Kind: fix")
			}
			gitIn(t, dir, args...)
		}
		got, err := app.FixCommit(dir, from)
		if err != nil || got != want {
			t.Fatalf("FixCommit over subjects %q = %v, %v; want %v: a commit is a fix exactly when its subject is typed fix under Conventional Commits, and the Assure-Kind trailer no longer counts", subjects, got, err, want)
		}
	})
}

func TestFixCommitIgnoresTheTrailer(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "feat(p): add Clamp", "-m", "Assure-Kind: fix")
	if got, err := app.FixCommit(dir, "HEAD~1"); err != nil || got {
		t.Fatalf("FixCommit = %v, %v for a feat commit with the Assure-Kind: fix trailer; the subject's type alone decides", got, err)
	}
}

func TestFixCommitIgnoresUncommittedWork(t *testing.T) {
	r := fixRepo(t)
	r.write("p/q.go", "package p\n")
	if got, err := app.FixCommit(r.root, "HEAD"); err != nil || got {
		t.Fatalf("FixCommit = %v, %v; uncommitted work carries no trailer", got, err)
	}
}

func TestEvaluateWithoutFixSubjectSkipsFailOnBase(t *testing.T) {
	r := fixRepo(t)
	r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
	commitWith(r, "feat(p): index from the end", "Assure-Kind: fix")
	assure("evaluate", "--changed-from", "HEAD~1")
	rep, _ := readReport(t, r)
	if e, ok := entry(rep, "VER-FAIL-ON-BASE", "go"); ok {
		t.Fatalf("VER-FAIL-ON-BASE in report for a feat commit carrying the old trailer: %+v; only a subject typed fix triggers it", e)
	}
	if _, err := os.Stat(filepath.Join(r.root, app.EvidenceDir, "evaluate", "go", "VER-FAIL-ON-BASE")); err == nil {
		t.Fatal("the fail-on-base adapter ran for a change that is not a fix")
	}
}

func TestEvaluateFixWithRegressionTestPasses(t *testing.T) {
	r := fixRepo(t)
	r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
	r.write("p/last_test.go", "package p\n\nimport \"testing\"\n\nfunc TestLastIsLast(t *testing.T) {\n\tif Last([]int{1, 2, 3}) != 3 {\n\t\tt.Fatal(\"off by one\")\n\t}\n}\n")
	commitWith(r, "fix(p): off-by-one in Last")
	_, stdout, stderr := assure("evaluate", "--changed-from", "HEAD~1")
	rep, _ := readReport(t, r)
	if e, ok := entry(rep, "VER-FAIL-ON-BASE", "go"); !ok || e.Status != "pass" {
		t.Fatalf("VER-FAIL-ON-BASE: %+v %v\n%s\n%s", e, ok, stdout, stderr)
	}
}

func TestEvaluateFixWithoutATest(t *testing.T) {
	for _, c := range []struct {
		setting string
		status  core.Status
		detail  string
	}{
		{"", "pass", "require_fix_test"},
		{"require_fix_test: true\n", "fail", "changes no test"},
	} {
		t.Run(string(c.status), func(t *testing.T) {
			r := fixRepo(t)
			if c.setting != "" {
				r.write("assurance.yaml", levelB+c.setting)
				commitWith(r, "chore: require a test with every fix")
			}
			r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
			commitWith(r, "fix(p): off-by-one in Last")
			code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD~1")
			if code == 2 {
				t.Fatalf("evaluate exit 2; a manifest with %q must load\n%s", c.setting, stderr)
			}
			rep, _ := readReport(t, r)
			e, ok := entry(rep, "VER-FAIL-ON-BASE", "go")
			if !ok || e.Status != c.status || !strings.Contains(strings.Join(e.Details, "\n"), c.detail) {
				t.Fatalf("VER-FAIL-ON-BASE %+v (present %v), want %s with a detail naming %q; a fix that changes no test passes with a recommendation unless the manifest sets require_fix_test\n%s\n%s", e, ok, c.status, c.detail, stdout, stderr)
			}
		})
	}
}

func TestEvaluateFixWhoseTestPassesOnBaseFails(t *testing.T) {
	r := fixRepo(t)
	r.write("p/p.go", strings.Replace(lastSrc, "len(xs)-2", "len(xs)-1", 1))
	r.write("p/last_test.go", "package p\n\nimport \"testing\"\n\nfunc TestLastNonZero(t *testing.T) {\n\tif Last([]int{1, 2, 3}) == 0 {\n\t\tt.Fatal(\"zero\")\n\t}\n}\n")
	commitWith(r, "fix(p): off-by-one in Last")
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
