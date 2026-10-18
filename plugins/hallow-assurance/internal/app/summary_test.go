package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var update = flag.Bool("update", false, "rewrite testdata/summary.golden.md")

func goldenReport() EvalReport {
	sha := "7330276f12e1aaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var many []string
	for i := range maxDetails + 2 {
		many = append(many, fmt.Sprintf("internal/x%02d.go: noctx: use CommandContext", i))
	}
	w := core.Waiver{Objective: "IND-VERIFIER-DISTINCT", Scope: "**", Rationale: "provenance arrives in milestone M4", Approver: "owner", Expires: "2026-12-31"}
	return EvalReport{
		Commit: sha, ChangedFrom: ChangedFrom{Ref: "origin/master", SHA: sha}, Date: "2026-09-28", Catalog: "v0", ChangedFiles: 3,
		Authority:         core.AuthorityCI,
		ToolVersions:      map[string]map[string]string{"go": {"go": "go1.27.1"}},
		ExpiredWaivers:    []core.Waiver{{Objective: "VER-FAIL-ON-BASE", Scope: "**", Rationale: "fail-on-base runner arrives in M3b", Approver: "owner", Expires: "2026-09-01"}},
		RemovableBaseline: []RemovableEntry{{Entry: core.BaselineEntry{Objective: "CODE-RESOURCE-BOUNDS", Rule: "noctx", Path: "internal/app/check.go", Message: "use CommandContext", Count: 2}, Unused: 1}},
		Objectives: []Entry{
			{Objective: "CODE-COMPLEXITY", Language: "go", Status: core.Pass, UnderThreshold: 40},
			{Objective: "CODE-RESOURCE-BOUNDS", Language: "go", Status: core.Fail, Details: many},
			{Objective: "IND-VERIFIER-DISTINCT", Status: core.Waived, Details: []string{"waived: no adapter lists IND-VERIFIER-DISTINCT"}, Waivers: []core.Waiver{w}},
			{Objective: "VER-TESTS-PASS", Language: "go", Status: core.Fail, Details: []string{"failing test: example.com/m/p.TestX: boom\np_test.go:9: boom"}},
			{Objective: "FM-COMPLETE", Status: core.AdvisoryFail, Details: []string{"no adapter lists FM-COMPLETE"}},
		},
	}
}

func TestSummaryGolden(t *testing.T) {
	got := goldenReport().Summary()
	const file = "testdata/summary.golden.md"
	if *update {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("summary differs from %s:\n%s", file, got)
	}
}

func TestSummaryNeedsOnlyTheStoredReport(t *testing.T) {
	rep := goldenReport()
	data, err := rep.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var stored EvalReport
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Summary() != rep.Summary() {
		t.Fatalf("summary from stored report differs:\n%s\n---\n%s", stored.Summary(), rep.Summary())
	}
}

func TestSummaryOmitsSectionsWithNothingToList(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep := goldenReport()
		rep.Objectives = rep.Objectives[:rapid.IntRange(0, len(rep.Objectives)).Draw(t, "objectives")]
		rep.ExpiredWaivers = rep.ExpiredWaivers[:rapid.IntRange(0, len(rep.ExpiredWaivers)).Draw(t, "expired")]
		rep.RemovableBaseline = rep.RemovableBaseline[:rapid.IntRange(0, len(rep.RemovableBaseline)).Draw(t, "removable")]
		got := rep.Summary()
		sections := []struct {
			heading string
			n       int
		}{
			{"| Objective | Language | Status |", len(rep.Objectives)},
			{"### Expired waivers", len(rep.ExpiredWaivers)},
			{"### Removable baseline entries", len(rep.RemovableBaseline)},
		}
		for _, s := range sections {
			if present := strings.Contains(got, s.heading); present != (s.n > 0) {
				t.Fatalf("heading %q present=%v with %d entries: an empty section tells the reviewer there is something to act on\n%s", s.heading, present, s.n, got)
			}
		}
	})
}

func drawVersions(t *rapid.T, label string) map[string]map[string]string {
	if rapid.Bool().Draw(t, label+" nil") {
		return nil
	}
	name := rapid.StringMatching(`[a-z]{1,6}`)
	return rapid.MapOfN(name, rapid.MapOfN(name, rapid.StringMatching(`[a-z0-9.]{1,8}`), 0, 2), 0, 2).Draw(t, label)
}

func TestMarshalKeepsToolVersionsAndPinsAndWritesAbsentOnesAsEmpty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep := goldenReport()
		rep.ToolVersions = drawVersions(t, "tool versions")
		rep.PinnedBy = drawVersions(t, "pinned by")
		data, err := rep.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v: a report with no tool versions or pins must still be written", err)
		}
		var stored EvalReport
		if err := json.Unmarshal(data, &stored); err != nil {
			t.Fatal(err)
		}
		for _, m := range []struct {
			name    string
			in, out map[string]map[string]string
		}{{"tool_versions", rep.ToolVersions, stored.ToolVersions}, {"pinned_by", rep.PinnedBy, stored.PinnedBy}} {
			if m.out == nil || !maps.EqualFunc(m.in, m.out, maps.Equal) {
				t.Fatalf("%s stored as %v, want %v: the report is the only record of which tool versions produced the verdict", m.name, m.out, m.in)
			}
		}
	})
}

func TestMarshalRefusesReportsThatViolateTheSchema(t *testing.T) {
	breaks := map[string]func(*EvalReport){
		"catalog":       func(r *EvalReport) { r.Catalog = "1" },
		"changed files": func(r *EvalReport) { r.ChangedFiles = -1 },
		"commit":        func(r *EvalReport) { r.Commit = "abc" },
		"empty problem": func(r *EvalReport) { r.Problems = []string{""} },
		"status":        func(r *EvalReport) { r.Objectives[0].Status = "bogus" },
	}
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom(slices.Sorted(maps.Keys(breaks))).Draw(t, "violation")
		rep := goldenReport()
		breaks[name](&rep)
		data, err := rep.Marshal()
		if err == nil || !strings.Contains(err.Error(), "report violates report.schema.json") || data != nil {
			t.Fatalf("Marshal with broken %s = %q, %v: a report consumers cannot validate must never be written", name, data, err)
		}
	})
}

var untrackedToolNames = []string{"golangci-lint", "gremlins", "vitest", "@stryker-mutator/core"}

func drawUntrackedPins(t *rapid.T) map[string][]string {
	if rapid.Bool().Draw(t, "pins nil") {
		return nil
	}
	pins := map[string][]string{}
	for _, lang := range rapid.SliceOfDistinct(rapid.SampledFrom([]string{"go", "typescript"}), rapid.ID[string]).Draw(t, "langs") {
		tools := rapid.SliceOfNDistinct(rapid.SampledFrom(untrackedToolNames), 1, len(untrackedToolNames), rapid.ID[string]).Draw(t, "tools "+lang)
		slices.Sort(tools)
		pins[lang] = tools
	}
	return pins
}

func TestSummaryLabelsALocalVerdictSelfAttested(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep := goldenReport()
		rep.Authority = rapid.SampledFrom([]core.Authority{core.AuthorityLocal, core.AuthorityCI}).Draw(t, "authority")
		rep.UntrackedPins = drawUntrackedPins(t)
		got := rep.Summary()
		first, _, _ := strings.Cut(got, "\n")
		local := rep.Authority == core.AuthorityLocal
		if strings.Contains(first, "self-attested") != local {
			t.Fatalf("authority %s, first line %q: a local verdict must say so on its first line, since no CI rerun stands behind it", rep.Authority, first)
		}
		if !local && strings.Contains(got, "self-attested") {
			t.Fatalf("authority ci, summary says self-attested:\n%s", got)
		}
	})
}

func TestSummaryListsUntrackedPins(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep := goldenReport()
		rep.Authority = rapid.SampledFrom([]core.Authority{core.AuthorityLocal, core.AuthorityCI}).Draw(t, "authority")
		rep.UntrackedPins = drawUntrackedPins(t)
		got := rep.Summary()
		lower := strings.ToLower(got)
		at := strings.Index(lower, "untracked")
		if (at >= 0) != (len(rep.UntrackedPins) > 0) {
			t.Fatalf("untracked pins %v, summary mentions untracked at %d:\n%s", rep.UntrackedPins, at, got)
		}
		for lang, tools := range rep.UntrackedPins {
			for _, tool := range tools {
				if !strings.Contains(got[at:], tool) {
					t.Fatalf("untracked %s pin %s not listed after the untracked heading; a reader of a local verdict must see which pins CI could not reproduce:\n%s", lang, tool, got)
				}
			}
		}
	})
}

func TestMarshalKeepsAuthorityAndUntrackedPins(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep := goldenReport()
		rep.Authority = rapid.SampledFrom([]core.Authority{core.AuthorityLocal, core.AuthorityCI}).Draw(t, "authority")
		rep.UntrackedPins = drawUntrackedPins(t)
		data, err := rep.Marshal()
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		var authority string
		var pins map[string][]string
		if err := json.Unmarshal(raw["authority"], &authority); err != nil || authority != string(rep.Authority) {
			t.Fatalf("authority stored as %s (%v), want %q", raw["authority"], err, rep.Authority)
		}
		if err := json.Unmarshal(raw["untracked_pins"], &pins); err != nil || pins == nil {
			t.Fatalf("untracked_pins stored as %s (%v); the key must always be an object so a consumer can tell none from not recorded", raw["untracked_pins"], err)
		}
		want := rep.UntrackedPins
		if want == nil {
			want = map[string][]string{}
		}
		if !maps.EqualFunc(pins, want, slices.Equal) {
			t.Fatalf("untracked_pins stored as %v, want %v", pins, want)
		}
	})
}
