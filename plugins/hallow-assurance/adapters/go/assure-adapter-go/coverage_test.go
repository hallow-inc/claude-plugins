package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func TestFuncSpansNameMethodsAsRecvDotMethod(t *testing.T) {
	ident := rapid.StringMatching(`[A-Z][a-z]{1,6}`)
	rapid.Check(t, func(t *rapid.T) {
		typ, meth := ident.Draw(t, "type"), ident.Draw(t, "method")
		recv := typ
		switch rapid.IntRange(0, 3).Draw(t, "shape") {
		case 1:
			recv = "*" + typ
		case 2:
			recv = typ + "[T]"
		case 3:
			recv = "*" + typ + "[K, V]"
		}
		pad := rapid.IntRange(0, 5).Draw(t, "pad")
		src := "package p\n\n" + strings.Repeat("// c\n", pad) + fmt.Sprintf("func (s %s) %s() {\n", recv, meth) + strings.Repeat("\t_ = 1\n", pad) + "}\n"
		fns, err := funcSpans([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		start := 3 + pad
		want := coverFunc{name: typ + "." + meth, start: start, end: start + pad + 1}
		if len(fns) != 1 || fns[0] != want {
			t.Fatalf("got %+v, want %+v\n%s", fns, want, src)
		}
	})
}

func TestProfileConvertsToLCOVWithPerLineMax(t *testing.T) {
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	pkgDir := filepath.Join(root, "sub", "p")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package p\n\nfunc A() {\n" + strings.Repeat("\t_ = 1\n", 10) + "}\n\nfunc (s *S) B() {\n" + strings.Repeat("\t_ = 2\n", 10) + "}\n"
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkgs := []goPackage{{importPath: "example.com/m/sub/p", dir: pkgDir, files: []string{"a.go", "b.go"}}}
	type block struct {
		file          string
		sl, el, count int
	}
	blockGen := rapid.Custom(func(t *rapid.T) block {
		sl := rapid.IntRange(1, 30).Draw(t, "sl")
		return block{
			file:  rapid.SampledFrom([]string{"a.go", "b.go"}).Draw(t, "file"),
			sl:    sl,
			el:    sl + rapid.IntRange(0, 5).Draw(t, "len"),
			count: rapid.IntRange(0, 4).Draw(t, "count"),
		}
	})
	rapid.Check(t, func(t *rapid.T) {
		blocks := rapid.SliceOf(blockGen).Draw(t, "blocks")
		var prof strings.Builder
		prof.WriteString("mode: set\n")
		want := map[string]map[int]int{"a.go": {}, "b.go": {}}
		for _, b := range blocks {
			fmt.Fprintf(&prof, "example.com/m/sub/p/%s:%d.2,%d.9 1 %d\n", b.file, b.sl, b.el, b.count)
			for l := b.sl; l <= b.el; l++ {
				want[b.file][l] = max(want[b.file][l], b.count)
			}
		}
		hits, err := parseProfile([]byte(prof.String()))
		if err != nil {
			t.Fatal(err)
		}
		files, err := convertProfile(root, pkgs, hits)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ev.ParseLCOV(formatLCOV(files))
		if err != nil {
			t.Fatalf("ParseLCOV rejected our LCOV: %v", err)
		}
		if len(parsed) != 2 {
			t.Fatalf("records %+v, want one per source file", parsed)
		}
		for _, f := range parsed {
			name := filepath.Base(f.Path)
			if dir := filepath.Dir(f.Path); dir != "sub/p" {
				t.Fatalf("SF %q is not repo-relative", f.Path)
			}
			if !maps.Equal(f.Lines, want[name]) {
				t.Fatalf("%s lines %v, want %v", f.Path, f.Lines, want[name])
			}
			names := []string{f.Functions[0].Name, f.Functions[1].Name}
			if !slices.Equal(names, []string{"A", "S.B"}) {
				t.Fatalf("functions %v", names)
			}
		}
	})
}

const signSrc = "package a\n\nfunc Sign(x int) int {\n\tif x < 0 {\n\t\treturn -1\n\t}\n\treturn 1\n}\n"
const signTest = "package a\n\nimport \"testing\"\n\nfunc TestSign(t *testing.T) {\n\tif Sign(2) != 1 {\n\t\tt.Fatal(\"sign\")\n\t}\n}\n"

func TestCoverageRunMarksUntestedBranchZeroAndIsDeterministic(t *testing.T) {
	dir := committedModule(t, map[string]string{
		"a/a.go":      "package a\n\nfunc Sign(x int) int {\n\treturn 1\n}\n",
		"a/a_test.go": signTest,
		"b/b.go":      "package b\n\nfunc B() int { return 1 }\n",
	})
	writeFiles(t, dir, map[string]string{"a/a.go": signSrc})
	lcov := func() []byte {
		out := t.TempDir()
		resp, stderr, code := runIn(t, dir, "run", "VER-COVERAGE-RESOLUTION", "--changed-from", "HEAD", "--out", out)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := lcov()
	if second := lcov(); string(first) != string(second) {
		t.Fatalf("LCOV differs between runs:\n%s\n---\n%s", first, second)
	}
	files, err := ev.ParseLCOV(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "a/a.go" {
		t.Fatalf("records %+v; only the selected package's files belong", files)
	}
	f := files[0]
	if h, ok := f.Lines[5]; !ok || h != 0 {
		t.Fatalf("DA for the untested branch (line 5) = %d,%v; want a zero-hit record\n%s", h, ok, first)
	}
	if f.Lines[7] == 0 {
		t.Fatalf("line 7 is exercised but has no hits\n%s", first)
	}
	if len(f.Functions) != 1 || f.Functions[0].Name != "Sign" || f.Functions[0].Start != 3 || f.Functions[0].End != 8 {
		t.Fatalf("functions %+v", f.Functions)
	}
}
