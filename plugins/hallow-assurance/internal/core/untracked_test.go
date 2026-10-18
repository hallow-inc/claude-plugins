package core

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var (
	pinTools   = []string{"golangci-lint", "gremlins", "vitest", "eslint"}
	pinOrigins = []string{"mise.local.toml", "package-lock.json", "mise.toml", "default"}
)

func drawPins(t *rapid.T) (pinnedBy map[string]string, untracked []string) {
	pinnedBy = map[string]string{}
	for _, tool := range rapid.SliceOfDistinct(rapid.SampledFrom(pinTools), rapid.ID[string]).Draw(t, "pinned") {
		pinnedBy[tool] = rapid.SampledFrom(pinOrigins).Draw(t, "pin "+tool)
	}
	keys := slices.Sorted(maps.Keys(pinnedBy))
	if len(keys) > 0 {
		untracked = rapid.SliceOfNDistinct(rapid.SampledFrom(keys), 0, len(keys), rapid.ID[string]).Draw(t, "untracked")
	}
	return pinnedBy, untracked
}

func TestUntrackedPinFailsItsObjectiveOnlyUnderCI(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := worldGen(t)
		authority := rapid.SampledFrom([]Authority{AuthorityLocal, AuthorityCI}).Draw(t, "authority")
		pinnedBy, untracked := drawPins(t)
		problems := UntrackedPinProblems(authority, untracked, pinnedBy)
		if authority == AuthorityLocal || len(untracked) == 0 {
			if problems != nil {
				t.Fatalf("authority %s, untracked %v: problems %q, want none; under local the developer's own run is the verdict and an untracked pin changes nothing", authority, untracked, problems)
			}
			return
		}
		requireOneProblemPerUntrackedTool(t, problems, untracked, pinnedBy)
		requireUnlocatedFailure(t, w, problems)
	})
}

func requireOneProblemPerUntrackedTool(t *rapid.T, problems, untracked []string, pinnedBy map[string]string) {
	sorted := slices.Sorted(slices.Values(untracked))
	if len(problems) != len(sorted) {
		t.Fatalf("untracked %v: %d problems %q, want one per tool", untracked, len(problems), problems)
	}
	for i, tool := range sorted {
		p := problems[i]
		if !strings.Contains(p, tool) || !strings.Contains(p, pinnedBy[tool]) || !strings.Contains(p, "CI cannot see") {
			t.Fatalf("problem %d %q must name %s, its pinned_by %s, and say CI cannot see the pin (sorted by tool: %v)", i, p, tool, pinnedBy[tool], sorted)
		}
	}
}

func requireUnlocatedFailure(t *rapid.T, w world, problems []string) {
	before := w.decide()
	with := w
	with.ev.Problems = slices.Concat(w.ev.Problems, problems)
	after := with.decide()
	if want := evalOracle(with); after.Status != want {
		t.Fatalf("status %s, rules for unlocated failures say %s (before the pins: %s)", after.Status, want, before.Status)
	}
	req, _, applicable := w.oracleApplicable()
	if !req || oracleCovered(w.oracleActive(), applicable) {
		return
	}
	if after.Status != Fail {
		t.Fatalf("an objective required at a changed file whose run used a pin CI cannot see must fail under authority: ci; got %s", after.Status)
	}
	for _, p := range problems {
		if !slices.Contains(after.Details, p) {
			t.Fatalf("details %q lack %q; the reader must learn which pin to commit", after.Details, p)
		}
	}
}
