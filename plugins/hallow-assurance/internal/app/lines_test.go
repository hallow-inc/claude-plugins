package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

var linePaths = []string{"a.go", "dir/b c.go", "q\"uote.go", "back\\slash.go", "tab\tname.go", "é.go", "deep/er/f.go"}

func gitIn(t fataler, root string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func putFile(t fataler, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

func tempRepo(t fataler) (root string, cleanup func()) {
	t.Helper()
	root, err := os.MkdirTemp("", "assure-lines")
	if err != nil {
		t.Fatalf("%v", err)
	}
	gitIn(t, root, "init", "-q")
	return root, func() { _ = os.RemoveAll(root) }
}

func isolateGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

func joinLines(ls []string) string {
	if len(ls) == 0 {
		return ""
	}
	return strings.Join(ls, "\n") + "\n"
}

func TestAddedLinesAreExactlyTheInsertedLines(t *testing.T) {
	isolateGit(t)
	inserted := []string{"new", "++ new", "-- new", "@@ -1 +1 @@ new", "+++ new", "diff --git new"}
	rapid.Check(t, func(t *rapid.T) {
		root, cleanup := tempRepo(t)
		defer cleanup()
		paths := rapid.SliceOfNDistinct(rapid.SampledFrom(linePaths), 1, 3, rapid.ID[string]).Draw(t, "paths")
		origs, edits, want := map[string]string{}, map[string]string{}, map[string][]int{}
		next := 0
		for _, p := range paths {
			var orig, edited []string
			for i := range rapid.IntRange(1, 10).Draw(t, "orig lines") {
				orig = append(orig, fmt.Sprintf("orig %d", i))
			}
			var added []int
			insert := func() {
				for range rapid.IntRange(0, 2).Draw(t, "inserts") {
					next++
					edited = append(edited, fmt.Sprintf("%s %d", rapid.SampledFrom(inserted).Draw(t, "prefix"), next))
					added = append(added, len(edited))
				}
			}
			for _, l := range orig {
				if rapid.Bool().Draw(t, "insert before") {
					insert()
				}
				if rapid.Bool().Draw(t, "keep") {
					edited = append(edited, l)
				}
			}
			if rapid.Bool().Draw(t, "insert at end") {
				insert()
			}
			origs[p], edits[p] = joinLines(orig), joinLines(edited)
			if len(added) > 0 {
				want[p] = added
			}
		}
		for p, c := range origs {
			putFile(t, root, p, c)
		}
		gitIn(t, root, "add", "-A")
		gitIn(t, root, "commit", "-qm", "base")
		for p, c := range edits {
			putFile(t, root, p, c)
		}
		got, err := AddedLines(root, "HEAD")
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, p := range paths {
			if !slices.Equal(got[p], want[p]) {
				t.Fatalf("%q: added lines %v, want %v\n--- before\n%s--- after\n%s", p, got[p], want[p], origs[p], edits[p])
			}
		}
		if len(got) > len(want) {
			t.Fatalf("lines reported for unedited or delete-only files: %v, want %v", got, want)
		}
	})
}

func TestAddedLinesOfNewFilesAreEveryLine(t *testing.T) {
	isolateGit(t)
	rapid.Check(t, func(t *rapid.T) {
		root, cleanup := tempRepo(t)
		defer cleanup()
		putFile(t, root, "base.txt", "base\n")
		gitIn(t, root, "add", "-A")
		gitIn(t, root, "commit", "-qm", "base")
		p := rapid.SampledFrom(linePaths).Draw(t, "path")
		n := rapid.IntRange(0, 8).Draw(t, "lines")
		var ls []string
		for i := range n {
			ls = append(ls, rapid.SampledFrom([]string{"x", "++ x", "+++ x"}).Draw(t, "line")+fmt.Sprint(i))
		}
		content := joinLines(ls)
		if n > 0 && !rapid.Bool().Draw(t, "trailing newline") {
			content = strings.TrimSuffix(content, "\n")
		}
		putFile(t, root, p, content)
		if rapid.Bool().Draw(t, "staged") {
			gitIn(t, root, "add", "--", p)
		}
		got, err := AddedLines(root, "HEAD")
		if err != nil {
			t.Fatalf("%v", err)
		}
		var want []int
		for i := range n {
			want = append(want, i+1)
		}
		if !slices.Equal(got[p], want) {
			t.Fatalf("new file %q (%d lines, %q): added %v, want %v", p, n, content, got[p], want)
		}
	})
}
