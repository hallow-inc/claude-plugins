package drt

import (
	"maps"
	"slices"
	"strings"

	"pgregory.net/rapid"
)

const objectiveID = "VER-X"

var (
	levelNames = []string{"A", "B", "C", "D"}
	needs      = []string{"", "required", "advisory"}
	appliesTo  = []string{"", "formal", "inputs", "fix"}
	otherRoles = []string{"test", "config", "generated", "fuzz_corpus", "unclassified"}
	statuses   = []string{"Killed", "Killed", "Timeout", "Survived", "NoCoverage", "Pending", "NotViable"}
	outcomes   = []string{"passed", "passed", "failed", "crashed"}
	dates      = []string{"2025-12-31", "2026-11-03", "2026-11-04", "2026-11-05", "2027-01-01"}
	dirSegs    = []string{"a", "b"}
	fileSegs   = []string{"a.go", "b.go", "ab.go", "a"}
	scopeSegs  = []string{"**", "**", "*", "?", "*.go", "?.go", "a*", "*b*", "a?go", "a", "b", "a.go", "b.go", "ab.go"}
	thresholds = []int{0, 1, 50, 66, 67, 75, 80, 99, 100}
)

func fewOf[T any](xs []T) *rapid.Generator[T] { return rapid.SampledFrom(xs) }

func smallCount() *rapid.Generator[int] { return rapid.SampledFrom([]int{0, 0, 0, 1, 2}) }

func nonNil[T any](xs []T) []T { return append([]T{}, xs...) }

func genPath() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		dirs := rapid.SliceOfN(fewOf(dirSegs), 0, 2).Draw(t, "dirs")
		return strings.Join(append(dirs, fewOf(fileSegs).Draw(t, "file")), "/")
	})
}

func genScope() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		return strings.Join(rapid.SliceOfN(fewOf(scopeSegs), 1, 3).Draw(t, "segs"), "/")
	})
}

func genObjective(t *rapid.T) objective {
	o := objective{ID: objectiveID, Levels: map[string]string{}, Threshold: map[string]int{}, AppliesTo: fewOf(appliesTo).Draw(t, "applies_to")}
	for _, l := range levelNames {
		if n := fewOf(needs).Draw(t, "need "+l); n != "" {
			o.Levels[l] = n
		}
		if rapid.Bool().Draw(t, "has threshold "+l) {
			o.Threshold[l] = rapid.OneOf(fewOf(thresholds), rapid.IntRange(0, 100)).Draw(t, "threshold "+l)
		}
	}
	return o
}

func genChanged(t *rapid.T) changedFile {
	role := "source"
	if rapid.Bool().Draw(t, "non-source") {
		role = fewOf(otherRoles).Draw(t, "role")
	}
	return changedFile{
		Path:   genPath().Draw(t, "path"),
		Level:  fewOf(levelNames).Draw(t, "level"),
		Role:   role,
		Formal: rapid.Bool().Draw(t, "formal"),
		Inputs: rapid.Bool().Draw(t, "inputs"),
		Fix:    rapid.Bool().Draw(t, "fix"),
		Lines:  nonNil(rapid.SliceOfN(rapid.IntRange(1, 5), 0, 4).Draw(t, "lines")),
	}
}

func genBase(t *rapid.T) *baseRun {
	if rapid.Bool().Draw(t, "no base") {
		return nil
	}
	return &baseRun{
		Failed:      smallCount().Draw(t, "failed"),
		Passed:      smallCount().Draw(t, "passed"),
		Skipped:     smallCount().Draw(t, "skipped"),
		Errored:     smallCount().Draw(t, "errored"),
		RequireTest: rapid.Bool().Draw(t, "require_test"),
	}
}

func genMutants(t *rapid.T, changed []changedFile) []mutant {
	out := []mutant{}
	if len(changed) == 0 {
		return out
	}
	for range rapid.IntRange(0, 6).Draw(t, "mutants") {
		f := changed[rapid.IntRange(0, len(changed)-1).Draw(t, "mutant file")]
		out = append(out, mutant{Path: f.Path, Level: f.Level, Status: fewOf(statuses).Draw(t, "status")})
	}
	return out
}

func genSomePath(t *rapid.T, changed []changedFile) string {
	if len(changed) > 0 && !rapid.Bool().Draw(t, "unchanged path") {
		return changed[rapid.IntRange(0, len(changed)-1).Draw(t, "changed index")].Path
	}
	return genPath().Draw(t, "other path")
}

func genFuzz(t *rapid.T, changed []changedFile) *fuzzRun {
	if rapid.Bool().Draw(t, "no fuzz") {
		return nil
	}
	run := &fuzzRun{Suites: []fuzzSuite{}}
	for range rapid.IntRange(0, 3).Draw(t, "suites") {
		s := fuzzSuite{Sources: []string{}, Cases: nonNil(rapid.SliceOfN(fewOf(outcomes), 0, 3).Draw(t, "cases"))}
		for range rapid.IntRange(0, 2).Draw(t, "sources") {
			s.Sources = append(s.Sources, genSomePath(t, changed))
		}
		run.Suites = append(run.Suites, s)
	}
	return run
}

func genCoverage(t *rapid.T, changed []changedFile) *coverage {
	if rapid.Bool().Draw(t, "no coverage") {
		return nil
	}
	cov := &coverage{Files: []coverFile{}}
	for range rapid.IntRange(0, 4).Draw(t, "cover files") {
		hits := rapid.MapOfN(rapid.IntRange(1, 5), fewOf([]int{0, 0, 1, 3}), 0, 5).Draw(t, "hits")
		f := coverFile{Path: genSomePath(t, changed), Hits: [][2]int{}}
		for _, l := range slices.Sorted(maps.Keys(hits)) {
			f.Hits = append(f.Hits, [2]int{l, hits[l]})
		}
		cov.Files = append(cov.Files, f)
	}
	return cov
}

func genWaivers(t *rapid.T) []waiver {
	out := []waiver{}
	for range rapid.IntRange(0, 3).Draw(t, "waivers") {
		out = append(out, waiver{
			Objective: fewOf([]string{objectiveID, objectiveID, "VER-Y"}).Draw(t, "waiver objective"),
			Scope:     genScope().Draw(t, "scope"),
			Expires:   fewOf(dates).Draw(t, "expires"),
		})
	}
	return out
}

func genCase() *rapid.Generator[modelCase] {
	return rapid.Custom(func(t *rapid.T) modelCase {
		changed := nonNil(rapid.SliceOfN(rapid.Custom(genChanged), 0, 4).Draw(t, "changed"))
		return modelCase{
			Objective: genObjective(t),
			Changed:   changed,
			Problems:  smallCount().Draw(t, "problems"),
			Failing:   smallCount().Draw(t, "failing"),
			Base:      genBase(t),
			Mutation:  rapid.Bool().Draw(t, "mutation"),
			Mutants:   genMutants(t, changed),
			Fuzz:      genFuzz(t, changed),
			Coverage:  genCoverage(t, changed),
			Waivers:   genWaivers(t),
			Date:      fewOf(dates[1:4]).Draw(t, "date"),
		}
	})
}
