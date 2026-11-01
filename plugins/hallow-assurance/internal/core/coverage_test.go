package core

import (
	"fmt"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func TestFunctionAtMatchesTheSpecSpans(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var cf CoverFile
		at := rapid.IntRange(1, 6).Draw(t, "first start")
		for i := range rapid.IntRange(0, 4).Draw(t, "nfuncs") {
			fn := CoverFunc{Name: fmt.Sprintf("fn%d", i), Start: at}
			span := rapid.IntRange(1, 8).Draw(t, "span")
			if rapid.Bool().Draw(t, "has end") {
				fn.End = at + span - 1
			}
			cf.Funcs = append(cf.Funcs, fn)
			at += span + rapid.IntRange(0, 3).Draw(t, "gap")
		}
		cf.Funcs = rapid.Permutation(cf.Funcs).Draw(t, "order")
		for line := 1; line <= at+1; line++ {
			if got, want := cf.functionAt(line), spanContaining(cf.Funcs, line); got != want {
				t.Fatalf("line %d of %+v attributed to %q, spec span says %q; a resolution for the wrong function would excuse or fail the line", line, cf.Funcs, got, want)
			}
		}
	})
}

func drawUncoveredWorld(t *rapid.T) (cf CoverFile, changed []int, res []Resolution) {
	cf = CoverFile{Path: "a.go", Lines: map[int]int{}}
	at := rapid.IntRange(1, 4).Draw(t, "first start")
	for i := range rapid.IntRange(0, 3).Draw(t, "nfuncs") {
		fn := CoverFunc{Name: fmt.Sprintf("fn%d", i), Start: at}
		span := rapid.IntRange(1, 5).Draw(t, "span")
		if rapid.Bool().Draw(t, "has end") {
			fn.End = at + span - 1
		}
		cf.Funcs = append(cf.Funcs, fn)
		at += span + rapid.IntRange(0, 2).Draw(t, "gap")
	}
	for line := 1; line <= at+1; line++ {
		if rapid.Bool().Draw(t, fmt.Sprintf("recorded %d", line)) {
			cf.Lines[line] = rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("hits %d", line))
		}
		if rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("changed %d", line)) > 0 {
			changed = append(changed, line)
		}
	}
	for _, fn := range cf.Funcs {
		if rapid.Bool().Draw(t, "resolve "+fn.Name) {
			res = append(res, Resolution{Path: "a.go", Function: fn.Name, Resolution: "dead"})
		}
	}
	return cf, changed, res
}

func expectedUncovered(cf CoverFile, changed []int, res []Resolution) []Uncovered {
	var want []Uncovered
	for _, line := range changed {
		if hits, ok := cf.Lines[line]; !ok || hits > 0 {
			continue
		}
		fn := spanContaining(cf.Funcs, line)
		if fn != "" && slices.ContainsFunc(res, func(r Resolution) bool { return r.Function == fn }) {
			continue
		}
		want = append(want, Uncovered{Line: line, Function: fn})
	}
	return want
}

func TestUncoveredLinesReportsLineAndEnclosingFunction(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cf, changed, res := drawUncoveredWorld(t)
		recorded := rapid.IntRange(0, 4).Draw(t, "has record") > 0
		c := Coverage{Resolutions: res}
		if recorded {
			c.Files = []CoverFile{cf}
		}
		f := ChangedFile{Path: "a.go", Level: "A", Role: Source, Lines: changed}

		got, _ := UncoveredLines(coverageObjective, []ChangedFile{f}, c)

		if !recorded {
			if len(got) != 1 || got[0].Line != 0 || got[0].Function != "" || got[0].Path != "a.go" || got[0].Level != "A" {
				t.Fatalf("no coverage record: got %+v, want one entry at line 0 with no function; audit reads line 0 as a file it could not audit", got)
			}
			return
		}
		var lines []Uncovered
		for _, u := range got {
			if u.Path != "a.go" || u.Level != "A" || !u.Located {
				t.Fatalf("entry %+v lost its file or level", u)
			}
			lines = append(lines, Uncovered{Line: u.Line, Function: u.Function})
		}
		if want := expectedUncovered(cf, changed, res); !slices.Equal(lines, want) {
			t.Fatalf("uncovered %+v, want %+v (funcs %+v, hits %v, changed %v, resolutions %+v); the line and its enclosing function, empty outside any function, are what an audit finding and its resolution name", lines, want, cf.Funcs, cf.Lines, changed, res)
		}
	})
}
