package core

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func bothOrders[T any](a, b T) [][]T {
	return [][]T{{a, b}, {b, a}}
}

func TestFuzzReadsAFailingSecondSuite(t *testing.T) {
	f := hookioFile()
	good := FuzzSuite{Sources: []string{f.Path}, Cases: []FuzzCase{{Name: "FuzzGood", Outcome: "passed"}}}
	bad := FuzzSuite{Sources: []string{f.Path}, Cases: []FuzzCase{{Name: "FuzzBad", Outcome: "failed"}}}
	for _, suites := range bothOrders(good, bad) {
		run := FuzzRun{Suites: suites}
		got := decideWith(fuzzObjective, []ChangedFile{f}, Evidence{Fuzz: &run}, nil)
		if got.Status != Fail || !slices.ContainsFunc(got.Details, func(d string) bool {
			return strings.Contains(d, f.Path) && strings.Contains(d, "FuzzBad")
		}) {
			t.Fatalf("suites %+v: got %+v; a failing case in any suite naming the file must fail it, whatever the order", suites, got)
		}
	}
}

func TestFuzzEmptySuiteBesideAPassingOnePasses(t *testing.T) {
	f := hookioFile()
	empty := FuzzSuite{Sources: []string{f.Path}}
	good := FuzzSuite{Sources: []string{f.Path}, Cases: []FuzzCase{{Name: "FuzzGood", Outcome: "passed"}}}
	for _, suites := range bothOrders(empty, good) {
		run := FuzzRun{Suites: suites}
		if got := decideWith(fuzzObjective, []ChangedFile{f}, Evidence{Fuzz: &run}, nil); got.Status != Pass {
			t.Fatalf("suites %+v: got %+v; the file has a passing target, so an empty sibling suite takes nothing away", suites, got)
		}
	}
}

func TestCoverageLineCoveredInAnotherRecord(t *testing.T) {
	decl := []CoverFunc{{Name: "DecideFast", Start: 30, End: 50}}
	zero := CoverFile{Path: fastPath, Funcs: decl, Lines: map[int]int{40: 0}}
	hit := CoverFile{Path: fastPath, Funcs: decl, Lines: map[int]int{40: 3}}
	f := ChangedFile{Path: fastPath, Level: "B", Role: Source, Lines: []int{40}}
	for _, files := range bothOrders(zero, hit) {
		c := Coverage{Files: files}
		if us, _ := UncoveredLines(coverageObjective, []ChangedFile{f}, c); len(us) != 0 {
			t.Fatalf("records %+v: uncovered %+v; hits sum across records, so line 40 ran 3 times", files, us)
		}
		if got := decideWith(coverageObjective, []ChangedFile{f}, Evidence{Coverage: &c}, nil); got.Status != Pass {
			t.Fatalf("records %+v: got %+v", files, got)
		}
	}
}

func TestCoverageLineUncoveredInEveryRecord(t *testing.T) {
	withFn := CoverFile{Path: fastPath, Funcs: []CoverFunc{{Name: "DecideFast", Start: 30, End: 50}}, Lines: map[int]int{40: 0}}
	bare := CoverFile{Path: fastPath, Lines: map[int]int{40: 0}}
	f := ChangedFile{Path: fastPath, Level: "B", Role: Source, Lines: []int{40}}
	for _, files := range bothOrders(withFn, bare) {
		c := Coverage{Files: files}
		got := decideWith(coverageObjective, []ChangedFile{f}, Evidence{Coverage: &c}, nil)
		if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), fastPath+":40") {
			t.Fatalf("records %+v: got %+v; a line with zero hits in every record is uncovered", files, got)
		}
		us, _ := UncoveredLines(coverageObjective, []ChangedFile{f}, c)
		if len(us) != 1 || us[0].Line != 40 || us[0].Function != "DecideFast" {
			t.Fatalf("records %+v: uncovered %+v; functions are the union of the records' FN spans, so line 40 lies in DecideFast", files, us)
		}
	}
}

func TestFuzzDecisionIgnoresSuiteOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o, changed, run, ws := fuzzWorldGen(t)
		perm := FuzzRun{Suites: rapid.Permutation(run.Suites).Draw(t, "order")}
		a, b := FuzzFindings(o, changed, run), FuzzFindings(o, changed, perm)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("suites %+v give %+v, reordered %+v give %+v", run.Suites, a, perm.Suites, b)
		}
		x, y := decideWith(o, changed, Evidence{Fuzz: &run}, ws), decideWith(o, changed, Evidence{Fuzz: &perm}, ws)
		if x.Status != y.Status {
			t.Fatalf("suite order flipped the decision: %s vs %s for %+v and %+v", x.Status, y.Status, run.Suites, perm.Suites)
		}
	})
}

func TestCoverageDecisionIgnoresRecordOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := coverWorldGen(t)
		perm := w.cov
		perm.Files = rapid.Permutation(w.cov.Files).Draw(t, "order")
		ua, ra := UncoveredLines(w.o, w.changed, w.cov)
		ub, rb := UncoveredLines(w.o, w.changed, perm)
		if !reflect.DeepEqual(ua, ub) || !reflect.DeepEqual(ra, rb) {
			t.Fatalf("records %+v give %+v %+v, reordered %+v give %+v %+v", w.cov.Files, ua, ra, perm.Files, ub, rb)
		}
		if a, b := RemovableResolutions(w.cov), RemovableResolutions(perm); !reflect.DeepEqual(a, b) {
			t.Fatalf("removable %+v, reordered %+v, for records %+v", a, b, w.cov.Files)
		}
	})
}
