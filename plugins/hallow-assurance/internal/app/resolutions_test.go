package app

import (
	"cmp"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func writeResolutions(t fataler, root string, rs []core.Resolution) {
	t.Helper()
	if len(rs) == 0 {
		return
	}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("%v", err)
	}
	putFile(t, root, core.ResolutionsFile, string(data))
}

func TestRemovableResolutionsAreThoseWhosePathOrFunctionIsGone(t *testing.T) {
	functions := []string{"F0", "F1"}
	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "assure-resolutions")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(root) }()
		var all, want []core.Resolution
		var cov core.Coverage
		for _, p := range rapid.SliceOfNDistinct(rapid.SampledFrom(evidencePaths), 0, 4, rapid.ID[string]).Draw(t, "paths") {
			present := rapid.Bool().Draw(t, "exists")
			recorded := rapid.Bool().Draw(t, "in lcov")
			if present {
				putFile(t, root, p, "package p\n")
			}
			cf := core.CoverFile{Path: p}
			have := map[string]bool{}
			for _, f := range functions {
				if rapid.Bool().Draw(t, "function in lcov") {
					cf.Funcs = append(cf.Funcs, core.CoverFunc{Name: f, Start: 1})
					have[f] = true
				}
			}
			if recorded {
				cov.Files = append(cov.Files, cf)
			}
			for _, f := range functions {
				if !rapid.Bool().Draw(t, "resolved") {
					continue
				}
				r := core.Resolution{Path: p, Function: f, Resolution: "dead", Rationale: "unreachable since the v2 migration", Approver: "@owner"}
				all = append(all, r)
				if !present || (recorded && !have[f]) {
					want = append(want, r)
				}
			}
		}
		writeResolutions(t, root, all)
		cov.Resolutions = all
		results := []evalResult{{}, {ev: core.Evidence{Coverage: &cov}}, {ev: core.Evidence{Coverage: &cov}}}
		var rep EvalReport
		got := removableResolutions(root, results, &rep)
		slices.SortFunc(want, func(a, b core.Resolution) int {
			return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Function, b.Function))
		})
		if len(rep.Problems) > 0 {
			t.Fatalf("problems %v", rep.Problems)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("removable %+v, want sorted and without duplicates %+v", got, want)
		}
	})
}

func TestSummaryListsAppliedAndRemovableResolutionsUnderTheirOwnHeadings(t *testing.T) {
	const applied, removable = "### Applied coverage resolutions", "### Removable coverage resolutions"
	gen := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) core.Resolution {
		return core.Resolution{
			Path:       rapid.SampledFrom(evidencePaths).Draw(t, "path"),
			Function:   rapid.SampledFrom([]string{"F0", "F1", "T.m"}).Draw(t, "function"),
			Resolution: rapid.SampledFrom([]string{"missing-test", "dead", "deactivated"}).Draw(t, "kind"),
			Rationale:  "first line\nsecond line of the rationale",
			Approver:   "@owner",
		}
	}), 0, 3)
	rapid.Check(t, func(t *rapid.T) {
		rep := goldenReport()
		a, r := gen.Draw(t, "applied"), gen.Draw(t, "removable")
		rep.Objectives[0].Resolutions = a
		rep.RemovableResolutions = r
		out := rep.Summary()
		ai, ri := strings.Index(out, applied), strings.Index(out, removable)
		if (ai >= 0) != (len(a) > 0) || (ri >= 0) != (len(r) > 0) {
			t.Fatalf("headings applied=%d removable=%d for %d applied and %d removable resolutions:\n%s", ai, ri, len(a), len(r), out)
		}
		section := func(from int, until int) string {
			if until < from {
				return out[from:]
			}
			return out[from:until]
		}
		for _, x := range a {
			if !strings.Contains(section(ai, ri), "`"+x.Path+"` "+x.Function+": "+x.Resolution) {
				t.Fatalf("applied %+v missing from its section:\n%s", x, out)
			}
		}
		for _, x := range r {
			if !strings.Contains(out[ri:], "`"+x.Path+"` "+x.Function+": "+x.Resolution) {
				t.Fatalf("removable %+v missing from its section:\n%s", x, out)
			}
		}
		if len(a) > 0 && !strings.Contains(out, "\n  second line of the rationale") {
			t.Fatalf("multi-line rationale not indented under its bullet:\n%s", out)
		}
	})
}
