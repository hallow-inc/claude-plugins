package adapterproto

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func validLanguageID(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

var adapterSuffixGen = rapid.OneOf(
	rapid.StringMatching(`[a-z][a-z0-9_-]{0,5}`),
	rapid.StringMatching(`[a-z]{0,3}[A-Z][a-zA-Z]{0,3}`),
	rapid.StringMatching(`[0-9_-][a-z0-9]{0,3}`),
	rapid.StringMatching(`[a-z]{1,3}\.[a-z]{1,3}`),
	rapid.Just(""),
)

const (
	absent = iota
	execFile
	plainFile
	directory
	foreignExec
)

func TestInstalledListsEachValidExecutableAdapterOnceSorted(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	iter := 0
	rapid.Check(t, func(rt *rapid.T) {
		iter++
		base := filepath.Join(root, fmt.Sprint(iter))
		nDirs := rapid.IntRange(1, 3).Draw(rt, "dirs")
		dirs := make([]string, nDirs)
		for i := range dirs {
			dirs[i] = filepath.Join(base, fmt.Sprint(i))
			if err := os.MkdirAll(dirs[i], 0o755); err != nil {
				rt.Fatal(err)
			}
		}
		suffixes := rapid.SliceOfNDistinct(adapterSuffixGen, 1, 6, strings.ToLower).Draw(rt, "suffixes")
		var want []string
		for _, s := range suffixes {
			installed := false
			for i, d := range dirs {
				if place(rt, d, s, rapid.IntRange(absent, foreignExec).Draw(rt, fmt.Sprintf("mode %q dir %d", s, i))) {
					installed = true
				}
			}
			if installed && validLanguageID(s) {
				want = append(want, s)
			}
		}
		slices.Sort(want)
		path := append(slices.Clone(dirs), filepath.Join(base, "missing"))
		if err := os.Setenv("PATH", strings.Join(path, string(os.PathListSeparator))); err != nil {
			rt.Fatal(err)
		}
		if got := Installed(); !slices.Equal(got, want) {
			rt.Fatalf("Installed() = %q, want %q (sorted, unique, valid lang id, executable file on PATH)", got, want)
		}
	})
}

func place(rt *rapid.T, dir, suffix string, mode int) bool {
	p := filepath.Join(dir, Executable(suffix))
	switch mode {
	case execFile:
		writeFile(rt, p, 0o755)
	case plainFile:
		writeFile(rt, p, 0o644)
	case directory:
		if err := os.Mkdir(p, 0o755); err != nil {
			rt.Fatal(err)
		}
	case foreignExec:
		writeFile(rt, filepath.Join(dir, "tool-"+suffix), 0o755)
	}
	return mode == execFile
}

func writeFile(rt *rapid.T, p string, mode os.FileMode) {
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		rt.Fatal(err)
	}
}
