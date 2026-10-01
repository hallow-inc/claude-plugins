package core

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var failOnBase = Objective{ID: "VER-FAIL-ON-BASE", Evidence: "test.fail_on_base", AppliesTo: "fix", Levels: map[Level]string{"A": "required", "B": "required", "C": "required", "D": "advisory"}}

func decideBase(b BaseRun) Outcome {
	return DecideObjective(failOnBase, []ChangedFile{{Path: "a.go", Level: "B", Fix: true}}, Evidence{Base: &b}, nil, nil, "2026-10-01")
}

func TestFailOnBaseEveryNewTestFails(t *testing.T) {
	if got := decideBase(BaseRun{Failed: 2}); got.Status != Pass {
		t.Fatalf("got %+v; both new tests reproduce the bug", got)
	}
}

func TestFailOnBaseNewTestPassesOnBase(t *testing.T) {
	got := decideBase(BaseRun{Failed: 1, Passed: []string{"pkg.TestQuoted"}})
	if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "passes on base: pkg.TestQuoted") {
		t.Fatalf("got %+v; a test that passes before the fix does not show the bug", got)
	}
}

func TestFailOnBaseBaseDoesNotCompile(t *testing.T) {
	got := decideBase(BaseRun{Errored: []string{"pkg.TestNew: undefined: Parse2"}})
	if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "undefined: Parse2") {
		t.Fatalf("got %+v; a test that needs the fix's new API does not isolate the bug", got)
	}
}

func TestFailOnBaseFixWithoutATest(t *testing.T) {
	got := decideBase(BaseRun{})
	if got.Status != Fail || !strings.Contains(strings.Join(got.Details, "\n"), "changes no test") {
		t.Fatalf("got %+v; a fix must start with a failing test", got)
	}
}

func TestFailOnBaseSkippedCaseFails(t *testing.T) {
	if got := decideBase(BaseRun{Failed: 1, Skipped: []string{"pkg.TestSkip"}}); got.Status != Fail {
		t.Fatalf("got %+v; a skipped test demonstrates nothing", got)
	}
}

func TestFailOnBasePassesExactlyWhenAllCasesFailed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		names := func(label string) []string {
			var out []string
			for i := range rapid.IntRange(0, 2).Draw(t, label) {
				out = append(out, fmt.Sprintf("%s%d", label, i))
			}
			return out
		}
		b := BaseRun{Failed: rapid.IntRange(0, 3).Draw(t, "failed"), Passed: names("passed"), Skipped: names("skipped"), Errored: names("errored")}
		cases := b.Failed + len(b.Passed) + len(b.Skipped) + len(b.Errored)
		if got, want := decideBase(b).Status == Pass, cases > 0 && b.Failed == cases; got != want {
			t.Fatalf("pass = %v, want %v for %+v", got, want, b)
		}
		ws := mustWaivers(t, waiverYAML("VER-FAIL-ON-BASE", "**", "2026-12-31"))
		waived := DecideObjective(failOnBase, []ChangedFile{{Path: "a.go", Level: "B", Fix: true}}, Evidence{Base: &b}, ws, nil, "2026-10-01")
		if want := map[bool]Status{true: Pass, false: Waived}[cases > 0 && b.Failed == cases]; waived.Status != want {
			t.Fatalf("with a covering waiver got %v, want %v", waived.Status, want)
		}
	})
}
