package adapterkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestExistsReportsFilesAndDirectoriesAndNotMissingPaths(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(root, "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		name := filepath.Join(dir, fileName().Draw(t, "name"))
		kind := rapid.SampledFrom([]string{"file", "dir", "missing"}).Draw(t, "kind")
		switch kind {
		case "file":
			writeFile(t, name, "x")
		case "dir":
			if err := os.MkdirAll(name, 0o755); err != nil {
				t.Fatalf("%v", err)
			}
		}
		if got, want := Exists(name), kind != "missing"; got != want {
			t.Fatalf("Exists(%s) = %v for a %s, want %v; adapters use it to find lockfiles, venvs and unit markers, so a wrong answer picks the wrong unit or reports a present tool as absent", name, got, kind, want)
		}
	})
}

func prefixed(prefix string) *rapid.Generator[string] {
	return rapid.Map(fileName(), func(s string) string { return prefix + s })
}

func TestRelSlashIsTheSlashSeparatedPathFromTop(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		top := "/" + strings.Join(rapid.SliceOfN(prefixed("t-"), 2, 3).Draw(t, "top"), "/")
		up := rapid.IntRange(0, 2).Draw(t, "up")
		segs := rapid.SliceOfN(prefixed("s-"), 0, 4).Draw(t, "segs")
		base := top
		want := []string{}
		for range up {
			base = filepath.Dir(base)
			want = append(want, "..")
		}
		want = append(want, segs...)
		if len(want) == 0 {
			want = []string{"."}
		}
		p := filepath.Join(append([]string{base}, segs...)...)
		if got := RelSlash(top, p); got != strings.Join(want, "/") {
			t.Fatalf("RelSlash(%q, %q) = %q, want %q; evidence and messages name files relative to the repository top with forward slashes", top, p, got, strings.Join(want, "/"))
		}
	})
}

func TestRelSlashReturnsPathUnchangedWhenItCannotBeMadeRelative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		top := "/" + strings.Join(rapid.SliceOfN(fileName(), 1, 3).Draw(t, "top"), "/")
		p := strings.Join(rapid.SliceOfN(fileName(), 1, 3).Draw(t, "p"), "/")
		if rapid.Bool().Draw(t, "swap") {
			top, p = p, top
		}
		if _, err := filepath.Rel(top, p); err == nil {
			t.Fatalf("filepath.Rel(%q, %q) succeeded; the fixture must mix absolute and relative paths", top, p)
		}
		if got := RelSlash(top, p); got != p {
			t.Fatalf("RelSlash(%q, %q) = %q, want %q unchanged; a path with no relation to top must still name something rather than come back empty", top, p, got, p)
		}
	})
}

func TestFindUpReturnsNearestMatchAtOrBelowTop(t *testing.T) {
	root := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		base, err := os.MkdirTemp(root, "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		top := filepath.Join(base, "top")
		depth := rapid.IntRange(0, 4).Draw(t, "depth")
		levels := []string{top}
		for i := range depth {
			levels = append(levels, filepath.Join(levels[i], fileName().Draw(t, "dir")))
		}
		unit := levels[depth]
		if err := os.MkdirAll(unit, 0o755); err != nil {
			t.Fatalf("%v", err)
		}
		rel := filepath.Join(rapid.SliceOfN(rapid.SampledFrom([]string{"uv.lock", "node_modules", ".bin", "pkg.json"}), 1, 2).Draw(t, "rel")...)
		asDir := rapid.Bool().Draw(t, "asDir")
		place := func(dir string) {
			p := filepath.Join(dir, rel)
			if asDir {
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatalf("%v", err)
				}
				return
			}
			writeFile(t, p, "x")
		}
		aboveTop := rapid.Bool().Draw(t, "aboveTop")
		if aboveTop {
			place(base)
		}
		want := ""
		for i := depth; i >= 0; i-- {
			if rapid.Bool().Draw(t, "placed") {
				place(levels[i])
				if want == "" {
					want = filepath.Join(levels[i], rel)
				}
			}
		}
		got, ok := FindUp(top, unit, rel)
		if ok != (want != "") || got != want {
			t.Fatalf("FindUp(top=%s, unit=%s, %s) = (%q, %v), want (%q, %v) (copy above top: %v); the nearest copy at or below top is the one the unit's tools use, and a copy outside the repository must never be picked up", top, unit, rel, got, ok, want, want != "", aboveTop)
		}
	})
}

func TestFindUpWalksToFilesystemRootWhenTopIsNotAnAncestor(t *testing.T) {
	root := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		base, err := os.MkdirTemp(root, "")
		if err != nil {
			t.Fatalf("%v", err)
		}
		unit := filepath.Join(base, fileName().Draw(t, "a"), fileName().Draw(t, "b"))
		if err := os.MkdirAll(unit, 0o755); err != nil {
			t.Fatalf("%v", err)
		}
		top := filepath.Join(base, "elsewhere")
		rel := "assure-findup-" + filepath.Base(base) + ".marker"
		placed := rapid.Bool().Draw(t, "placed")
		want := ""
		if placed {
			writeFile(t, filepath.Join(base, rel), "x")
			want = filepath.Join(base, rel)
		}
		type found struct {
			path string
			ok   bool
		}
		done := make(chan found, 1)
		go func() {
			p, ok := FindUp(top, unit, rel)
			done <- found{p, ok}
		}()
		var got string
		var ok bool
		select {
		case r := <-done:
			got, ok = r.path, r.ok
		case <-time.After(10 * time.Second):
			t.Fatalf("FindUp(top=%s, unit=%s, %s) did not return within 10s; a unit outside top must stop at the filesystem root instead of looping", top, unit, rel)
		}
		if ok != placed || got != want {
			t.Fatalf("FindUp(top=%s, unit=%s, %s) = (%q, %v), want (%q, %v); with top not above unit the walk ends at the filesystem root, finding any ancestor's copy and otherwise reporting not found", top, unit, rel, got, ok, want, placed)
		}
	})
}
