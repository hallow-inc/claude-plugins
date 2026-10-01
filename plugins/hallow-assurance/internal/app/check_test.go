package app

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func TestReportSeparatesFailuresTheAgentCannotFix(t *testing.T) {
	errs := map[string]error{
		"unstartable": fmt.Errorf("run: %w", core.ErrAdapterUnstartable),
		"outside":     fmt.Errorf("guard: %w", core.ErrSnapshotMissing),
		"plain":       errors.New("boom"),
	}
	rapid.Check(t, func(t *rapid.T) {
		var rep Report
		var wantFail []core.FailureKey
		var wantRemedies []string
		wantOutside := true
		for i := range rapid.IntRange(0, 5).Draw(t, "results") {
			res := Result{Lang: rapid.SampledFrom([]string{"go", "ts"}).Draw(t, "lang"), Keys: []string{fmt.Sprintf("k%d", i)}}
			res.ID = fmt.Sprintf("OBJ-%d", i)
			res.Status = rapid.SampledFrom([]core.Status{core.Pass, core.Fail}).Draw(t, "status")
			kinds := rapid.SliceOfN(rapid.SampledFrom([]string{"unstartable", "outside", "plain"}), 0, 2).Draw(t, "errs")
			unstartable, plain := false, false
			for _, k := range kinds {
				res.Errs = append(res.Errs, errs[k])
				unstartable = unstartable || k == "unstartable"
				plain = plain || k == "plain"
			}
			rep.Results = append(rep.Results, res)
			if res.Status != core.Fail {
				continue
			}
			wantFail = append(wantFail, core.FailureKey{Objective: res.ID, Lang: res.Lang, Keys: res.Keys})
			if len(kinds) == 0 || plain {
				wantOutside = false
			}
			if unstartable {
				wantRemedies = append(wantRemedies, "install "+adapterproto.Executable(res.Lang)+" on PATH, then restart Claude Code")
			}
		}
		slices.Sort(wantRemedies)
		wantRemedies = slices.Compact(wantRemedies)
		if got := rep.Failures(); !slices.EqualFunc(got, wantFail, func(a, b core.FailureKey) bool {
			return a.Objective == b.Objective && a.Lang == b.Lang && slices.Equal(a.Keys, b.Keys)
		}) {
			t.Fatalf("Failures() = %+v, want only the failing results in order: %+v", got, wantFail)
		}
		if got := rep.Blocking(); got != (len(wantFail) > 0) {
			t.Fatalf("Blocking() = %v with %d failures", got, len(wantFail))
		}
		if got := rep.OutsideReach(); got != wantOutside {
			t.Fatalf("OutsideReach() = %v, want %v: a failure with no error or a fixable error is the agent's to fix", got, wantOutside)
		}
		if got := rep.Remedies(); !slices.Equal(got, wantRemedies) {
			t.Fatalf("Remedies() = %q, want %q", got, wantRemedies)
		}
	})
}
