package adapterkit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func git(t fataler, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "core.excludesFile=/dev/null"}, args...)...) //nolint:noctx // rapid.T carries no context
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t fataler, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func committedRepo(t *testing.T) string {
	t.Helper()
	dir := realTempDir(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

const (
	unchanged = iota
	modified
	deleted
	untracked
	ignored
)

// fileName draws names git quotes under the default core.quotePath: non-ASCII, space,
// double quote, backslash, tab, newline, and leading or trailing space. '.' is excluded so
// no name can be ".", "..", ".git", or the ignored directory; '/' and NUL are invalid on APFS.
func fileName() *rapid.Generator[string] {
	runes := []rune{'a', 'B', 'z', '-', ' ', '"', '\\', '\t', '\n', 'é', '日', 'ß'}
	return rapid.StringOfN(rapid.SampledFrom(runes), 1, 8, -1)
}

func changeWorktree(t fataler, root, name string, state int) (changed bool) {
	t.Helper()
	switch state {
	case modified:
		writeFile(t, filepath.Join(root, name), "changed\n")
		return true
	case deleted:
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatalf("%v", err)
		}
		return true
	case untracked:
		writeFile(t, filepath.Join(root, name), "new\n")
		return true
	case ignored:
		writeFile(t, filepath.Join(root, "ign", name), "ignored\n")
	}
	return false
}

func TestChangedPathsReportsExactlyTheChangedNamesLiterally(t *testing.T) {
	isolateGitConfig(t)
	rapid.Check(t, func(rt *rapid.T) {
		// Case-folded keys keep names distinct on case-insensitive APFS, where "a" and "A"
		// are one file; rapid's shrinker otherwise makes such collisions near-certain.
		names := rapid.SliceOfNDistinct(fileName(), 1, 6, strings.ToLower).Draw(rt, "names")
		states := make([]int, len(names))
		for i := range names {
			states[i] = rapid.IntRange(unchanged, ignored).Draw(rt, "state")
		}
		inSub := rapid.Bool().Draw(rt, "inSub")

		repo := realTempDir(t)
		root := repo
		if inSub {
			root = filepath.Join(repo, "sub")
			writeFile(rt, filepath.Join(repo, "outside.txt"), "o\n")
		}
		writeFile(rt, filepath.Join(repo, ".gitignore"), "ign/\n")
		for i, n := range names {
			if states[i] == modified || states[i] == deleted || states[i] == unchanged {
				writeFile(rt, filepath.Join(root, n), "base\n")
			}
		}
		git(rt, repo, "init", "-q")
		git(rt, repo, "add", "-A")
		git(rt, repo, "commit", "-qm", "init")

		if inSub {
			writeFile(rt, filepath.Join(repo, "outside.txt"), "changed outside root\n")
			writeFile(rt, filepath.Join(repo, "outside-new.txt"), "new outside root\n")
		}
		var want []string
		for i, n := range names {
			if changeWorktree(rt, root, n, states[i]) {
				want = append(want, n)
			}
		}

		got, err := ChangedPaths(root, "HEAD")
		if err != nil {
			rt.Fatalf("ChangedPaths: %v", err)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			rt.Fatalf("ChangedPaths(root, HEAD) = %q, want %q (states %v, inSub %v); callers select the tests to run from these names, so a quoted, missing, extra, ignored, or non-root-relative name skips or misdirects tests", got, want, states, inSub)
		}
	})
}

func TestGitPathsSplitsOnNULAndDropsEmpties(t *testing.T) {
	isolateGitConfig(t)
	rapid.Check(t, func(rt *rapid.T) {
		names := rapid.SliceOfNDistinct(fileName(), 0, 6, strings.ToLower).Draw(rt, "names")
		dir := realTempDir(t)
		git(rt, dir, "init", "-q")
		for _, n := range names {
			writeFile(rt, filepath.Join(dir, n), "x\n")
		}
		got, err := GitPaths(dir, "ls-files", "--others")
		if err != nil {
			rt.Fatalf("GitPaths: %v", err)
		}
		slices.Sort(got)
		want := slices.Clone(names)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			rt.Fatalf("GitPaths(ls-files --others) = %q, want %q; without -z git quotes these names and splitting on newline breaks names that contain one", got, want)
		}
	})
}

func TestGitBytesErrorCarriesGitStderr(t *testing.T) {
	isolateGitConfig(t)
	dir := committedRepo(t)
	args := []string{"diff", "--name-only", "--relative", "no-such-ref", "--"}
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Run() == nil {
		t.Fatal("git diff against a missing ref succeeded; fixture is wrong")
	}
	gitSaid := strings.TrimSpace(stderr.String())
	if gitSaid == "" {
		t.Fatal("git printed nothing on stderr; fixture is wrong")
	}

	_, err := GitBytes(dir, args...)
	if err == nil || !strings.Contains(err.Error(), gitSaid) {
		t.Fatalf("GitBytes error = %v, want it to contain git's stderr %q; without it a bad ref is undiagnosable", err, gitSaid)
	}
	paths, err := ChangedPaths(dir, "no-such-ref")
	if err == nil || paths != nil {
		t.Fatalf("ChangedPaths with a bad ref = %q, %v; want nil paths and an error, since an empty change set would silently select no tests", paths, err)
	}
}

func TestGitBytesReturnsStdoutOnly(t *testing.T) {
	isolateGitConfig(t)
	dir := committedRepo(t)
	got, err := GitBytes(dir, "ls-files")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a.txt\n" {
		t.Fatalf("GitBytes(ls-files) = %q, want %q", got, "a.txt\n")
	}
}

func TestGitPathsReportsMissingGit(t *testing.T) {
	isolateGitConfig(t)
	dir := committedRepo(t)
	t.Setenv("PATH", t.TempDir())
	paths, err := GitPaths(dir, "ls-files")
	if err == nil || err.Error() != "git not found on PATH" || paths != nil {
		t.Fatalf("GitPaths without git = %q, %v; want error %q so the user sees a missing tool, not an empty change set", paths, err, "git not found on PATH")
	}
}
