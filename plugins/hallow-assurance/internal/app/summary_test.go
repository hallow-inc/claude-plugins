package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"

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
