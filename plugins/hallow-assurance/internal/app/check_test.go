package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func TestReportSeparatesFailuresTheAgentCannotFix(t *testing.T) {
	errs := map[string]error{
		"unstartable": fmt.Errorf("run: %w", core.ErrAdapterUnstartable),
		"environment": fmt.Errorf("run: %w", core.ErrToolEnvironment),
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
			kinds := rapid.SliceOfN(rapid.SampledFrom([]string{"unstartable", "environment", "plain"}), 0, 2).Draw(t, "errs")
			for _, k := range kinds {
				res.Errs = append(res.Errs, errs[k])
			}
			rep.Results = append(rep.Results, res)
			if res.Status != core.Fail {
				continue
			}
			wantFail = append(wantFail, core.FailureKey{Objective: res.ID, Lang: res.Lang, Keys: res.Keys})
			if len(kinds) == 0 || slices.Contains(kinds, "plain") {
				wantOutside = false
			}
			wantRemedies = append(wantRemedies, remediesFor(res.Lang, kinds)...)
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

func remediesFor(lang string, kinds []string) []string {
	var out []string
	if slices.Contains(kinds, "unstartable") {
		out = append(out, "install "+adapterproto.Executable(lang)+" on PATH, then restart Claude Code")
	}
	if slices.Contains(kinds, "environment") {
		out = append(out, "install the pinned tools with `assure tools --install-script | sh`, then restart Claude Code")
	}
	return out
}

func TestRenderSaysNoChangedFilesOnlyWhenNothingChangedAndNothingRan(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep := Report{Changed: rapid.IntRange(0, 2).Draw(t, "changed")}
		for i := range rapid.IntRange(0, 2).Draw(t, "results") {
			res := Result{Lang: "go"}
			res.ID = fmt.Sprintf("OBJ-%d", i)
			res.Status = rapid.SampledFrom([]core.Status{core.Pass, core.Fail}).Draw(t, "status")
			rep.Results = append(rep.Results, res)
		}
		out := rep.Render()
		if rep.Changed == 0 && len(rep.Results) == 0 {
			if out != "no changed files\n" {
				t.Fatalf("Render() = %q: an empty change must say so, not print an empty report the agent reads as a pass", out)
			}
			return
		}
		if strings.Contains(out, "no changed files") {
			t.Fatalf("Render() = %q with %d changed files and %d results: claiming nothing changed hides the results", out, rep.Changed, len(rep.Results))
		}
		for _, res := range rep.Results {
			if line := fmt.Sprintf("%s\t%s\t%s\n", res.Status, res.ID, res.Lang); !strings.Contains(out, line) {
				t.Fatalf("Render() = %q lacks the status line %q", out, line)
			}
		}
	})
}

func TestFailingTextShowsMessageAndOutputOnlyWhenPresent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := evidence.Failure{
			Name:    rapid.StringMatching(`[A-Za-z][A-Za-z0-9./]{0,8}`).Draw(t, "name"),
			Message: rapid.SampledFrom([]string{"", "boom", "want 1, got 2"}).Draw(t, "message"),
			Text:    rapid.SampledFrom([]string{"", " ", "\n\t\n", "p_test.go:9: boom", "\n  p_test.go:9: boom\n  more\n"}).Draw(t, "text"),
		}
		want := "failing test: " + f.Name
		if f.Message != "" {
			want += ": " + f.Message
		}
		if out := strings.TrimSpace(f.Text); out != "" {
			want += "\n" + out
		}
		if got := failingText(f); got != want {
			t.Fatalf("failingText(%+v) = %q, want %q: an absent message or blank output must not leave a dangling separator, and a present one must reach the agent", f, got, want)
		}
	})
}
