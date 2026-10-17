package qualification

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

const commitDate = "2026-01-01T00:00:00Z"

func git(ctx context.Context, dir string, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "core.excludesFile=/dev/null",
		"-c", "user.name=q", "-c", "user.email=q@q",
	}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+commitDate, "GIT_COMMITTER_DATE="+commitDate)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func writeTree(dir string, files map[string][]byte) error {
	for p, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func buildRepo(ctx context.Context, dir string, c qcase) (string, error) {
	if err := writeTree(dir, c.base); err != nil {
		return "", err
	}
	steps := [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "base"}}
	for _, s := range steps {
		if _, err := git(ctx, dir, "", s...); err != nil {
			return "", err
		}
	}
	base, err := git(ctx, dir, "", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if err := writeTree(dir, c.head); err != nil {
		return "", err
	}
	for _, p := range c.deletes {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			return "", err
		}
	}
	if _, err := git(ctx, dir, "", "add", "-A"); err != nil {
		return "", err
	}
	if _, err := git(ctx, dir, c.msg, "commit", "-q", "--allow-empty", "-F", "-"); err != nil {
		return "", err
	}
	return base, nil
}

func setupCase(ctx context.Context, dir, lang string, tools map[string]string, path string) error {
	step := caseSetup[lang]
	if len(step) == 0 {
		return nil
	}
	bin, ok := tools[step[0]]
	if !ok {
		return fmt.Errorf("%s cases run %q before evaluating, so %s cannot be omitted", lang, strings.Join(step, " "), step[0])
	}
	// The host path, not the tool-dir symlink: mise's npm wrapper finds node relative to the path it was invoked by.
	cmd := exec.CommandContext(ctx, bin, step[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(step, " "), err, out)
	}
	return nil
}

func hostTools(names []string) (map[string]string, error) {
	tools := map[string]string{}
	for _, n := range names {
		p, err := exec.LookPath(n)
		if err != nil {
			return nil, fmt.Errorf("qualification needs %s on PATH: %w", n, err)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		tools[n] = abs
	}
	return tools, nil
}

func toolDir(dir string, tools map[string]string) (string, error) {
	for name, target := range tools {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			return "", err
		}
	}
	return strings.Join([]string{dir, "/usr/bin", "/bin"}, string(os.PathListSeparator)), nil
}

func TestBuildRepoTwoCommitsTypedFix(t *testing.T) {
	c, err := parseCase("go", "typed-fix", []byte(`-- base/go.mod --
module example.com/m
-- base/p/keep.go --
package p
-- base/p/gone.go --
package p
-- head/p/keep.go --
package p

func F() {}
-- head/p/new.go --
package p
-- delete --
p/gone.go
-- head.msg --
fix(p): double correctly

Body line.
-- expect.json --
{"objectives": []}
`))
	if err != nil {
		t.Fatal(err)
	}
	dir := realTempDir(t)
	base, err := buildRepo(t.Context(), dir, c)
	if err != nil {
		t.Fatal(err)
	}
	q := func(args ...string) string {
		t.Helper()
		out, err := git(t.Context(), dir, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if n := q("rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("want 2 commits, got %s", n)
	}
	if parent := q("rev-parse", "HEAD~1"); parent != base {
		t.Fatalf("base sha %s is not the head's parent %s", base, parent)
	}
	if subject := q("log", "-1", "--format=%s"); subject != "fix(p): double correctly" {
		t.Fatalf("head subject = %q; the subject's fix type is what makes VER-FAIL-ON-BASE applicable to a case", subject)
	}
	if msg := q("log", "-1", "--format=%B"); msg != "fix(p): double correctly\n\nBody line." {
		t.Fatalf("head message = %q; the case's head.msg must be committed verbatim", msg)
	}
	if files := q("ls-tree", "-r", "--name-only", "HEAD"); files != "go.mod\np/keep.go\np/new.go" {
		t.Fatalf("head tree:\n%s", files)
	}
	if body := q("show", "HEAD:p/keep.go"); body != "package p\n\nfunc F() {}" {
		t.Fatalf("head p/keep.go = %q", body)
	}
	if files := q("ls-tree", "-r", "--name-only", base); files != "go.mod\np/gone.go\np/keep.go" {
		t.Fatalf("base tree:\n%s", files)
	}
	if status := q("status", "--porcelain"); status != "" {
		t.Fatalf("worktree not clean after head commit:\n%s", status)
	}
	head := q("rev-parse", "HEAD")
	t.Setenv("GIT_AUTHOR_DATE", "2001-02-03T04:05:06Z")
	t.Setenv("GIT_COMMITTER_DATE", "2001-02-03T04:05:06Z")
	again := realTempDir(t)
	if _, err := buildRepo(t.Context(), again, c); err != nil {
		t.Fatal(err)
	}
	if h, err := git(t.Context(), again, "", "rev-parse", "HEAD"); err != nil || h != head {
		t.Fatalf("rebuilding the case at another time gave head %s (err %v), want %s: the adapter seeds fuzz and mutation runs from the head SHA", h, err, head)
	}
}

func TestSetupRunsTheLanguageStepInTheCaseRoot(t *testing.T) {
	scratch := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		lang := rapid.SampledFrom(slices.Sorted(maps.Keys(caseSetup))).Draw(rt, "language")
		step := caseSetup[lang]
		if !slices.Contains(caseTools[lang], step[0]) {
			rt.Fatalf("%s setup runs %s, which is not one of its case tools %q", lang, step[0], caseTools[lang])
		}
		root, err := os.MkdirTemp(scratch, "case")
		if err != nil {
			rt.Fatal(err)
		}
		dir, err := filepath.EvalSymlinks(root)
		if err != nil {
			rt.Fatal(err)
		}
		code := rapid.IntRange(0, 3).Draw(rt, "exit code")
		logPath := filepath.Join(scratch, filepath.Base(root)+".log")
		bin := filepath.Join(scratch, filepath.Base(root)+"-"+step[0])
		script := fmt.Sprintf("#!/bin/sh\n{ pwd -P; printf '%%s\\n' \"$PATH\" \"$*\"; } > '%s'\nexit %d\n", logPath, code)
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			rt.Fatal(err)
		}
		path := rapid.StringMatching(`/[a-z]{1,6}(:/[a-z]{1,6}){0,2}`).Draw(rt, "path") + ":/usr/bin:/bin"
		err = setupCase(context.Background(), dir, lang, map[string]string{step[0]: bin}, path)
		if (err != nil) != (code != 0) {
			rt.Fatalf("setup exiting %d returned error %v; a failed install must stop the case, a clean one must not", code, err)
		}
		logged, rerr := os.ReadFile(logPath)
		if rerr != nil {
			rt.Fatalf("setup never ran %s: %v", step[0], rerr)
		}
		if want := dir + "\n" + path + "\n" + strings.Join(step[1:], " ") + "\n"; string(logged) != want {
			rt.Fatalf("setup ran with cwd, PATH, args %q, want %q", logged, want)
		}
		if err := setupCase(context.Background(), dir, lang, map[string]string{}, path); err == nil || !strings.Contains(err.Error(), "cannot be omitted") {
			rt.Fatalf("setup without %s returned %v, want a refusal", step[0], err)
		}
	})
}

func TestToolDirExposesExactlyTheGivenTools(t *testing.T) {
	src := t.TempDir()
	names := []string{"qualtool-a", "qualtool-b", "qualtool-c"}
	all := map[string]string{}
	for _, n := range names {
		p := filepath.Join(src, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+n+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		all[n] = p
	}
	for _, omitted := range names {
		given := map[string]string{}
		for n, p := range all {
			if n != omitted {
				given[n] = p
			}
		}
		dir := t.TempDir()
		path, err := toolDir(dir, given)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", path)
		if p, err := exec.LookPath(omitted); !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("omitted %s resolved to %q (err %v)", omitted, p, err)
		}
		for n := range given {
			p, err := exec.LookPath(n)
			if err != nil || p != filepath.Join(dir, n) {
				t.Fatalf("%s resolved to %q (err %v), want %s", n, p, err, filepath.Join(dir, n))
			}
			out, err := exec.CommandContext(t.Context(), n).Output()
			if err != nil || strings.TrimSpace(string(out)) != n {
				t.Fatalf("running %s via PATH: out %q err %v", n, out, err)
			}
		}
		if !strings.HasSuffix(path, string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin") {
			t.Fatalf("PATH %q must end with /usr/bin and /bin", path)
		}
	}
}
