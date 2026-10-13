package core

import (
	"fmt"
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
