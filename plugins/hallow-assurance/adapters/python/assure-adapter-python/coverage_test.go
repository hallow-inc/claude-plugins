package main

import (
	"maps"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

// TestLCOVFileMatchesModel: DA has one record per executable line,
// executed lines 1 and missing lines 0 (the Go -covermode=set clamp), and
// FN keeps the helper's names and spans in order; a function counts as hit
// when a line after its def line up to its end executed, since the def line
// itself runs at import.
func TestLCOVFileMatchesModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lines := rapid.SliceOfNDistinct(rapid.IntRange(1, 40), 0, 25, rapid.ID[int]).Draw(t, "lines")
		var f coverageFile
		want := map[int]int{}
		for _, l := range lines {
			if rapid.Bool().Draw(t, "executed") {
				f.ExecutedLines = append(f.ExecutedLines, l)
				want[l] = 1
			} else {
				f.MissingLines = append(f.MissingLines, l)
				want[l] = 0
			}
		}
		var funcs []pyFunc
		for i := range rapid.IntRange(0, 4).Draw(t, "funcs") {
			start := rapid.IntRange(1, 40).Draw(t, "start")
			funcs = append(funcs, pyFunc{Name: "f" + string(rune('a'+i)), Start: start, End: start + rapid.IntRange(0, 6).Draw(t, "len")})
		}
		got := lcovFile("app/x.py", f, funcs)
		if got.Path != "app/x.py" || !maps.Equal(got.Lines, want) {
			t.Fatalf("lcovFile path %q lines %v, want app/x.py %v", got.Path, got.Lines, want)
		}
		if len(got.Functions) != len(funcs) {
			t.Fatalf("%d functions, want %d", len(got.Functions), len(funcs))
		}
		for i, fn := range funcs {
			hit := 0
			if slices.ContainsFunc(f.ExecutedLines, func(l int) bool { return l > fn.Start && l <= fn.End }) {
				hit = 1
			}
			g := got.Functions[i]
			if g.Name != fn.Name || g.Start != fn.Start || g.End != fn.End || g.Hits != hit {
				t.Fatalf("function %d = %+v, want %s %d-%d hits %d", i, g, fn.Name, fn.Start, fn.End, hit)
			}
		}
	})
}
