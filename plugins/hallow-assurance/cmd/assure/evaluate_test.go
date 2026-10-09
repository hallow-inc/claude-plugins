package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const unbuiltWaivers = `- {objective: IND-VERIFIER-DISTINCT, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: CFG-PROTECTED, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: VER-MUTATION-CHANGED, scope: '**', rationale: mutation runner arrives in M3b, approver: owner, expires: 2099-01-01}
- {objective: VER-FAIL-ON-BASE, scope: '**', rationale: fail-on-base runner arrives in M3b, approver: owner, expires: 2099-01-01}
- {objective: VER-TEST-BUDGET, scope: '**', rationale: test budget check arrives in M3b, approver: owner, expires: 2099-01-01}
- {objective: VER-ROBUST-FUZZ, scope: '**', rationale: fuzz evidence arrives in milestone M3b, approver: owner, expires: 2099-01-01}
`

const levelBReviewed = levelB + "human_review: {B: required}\n"

func reviewedGoRepo(t *testing.T) repo {
	t.Helper()
	r := goRepo(t)
	r.write("assurance.yaml", levelBReviewed)
	r.git("commit", "-qam", "require review")
	return r
}

func waivedGoRepo(t *testing.T) repo {
	t.Helper()
	r := reviewedGoRepo(t)
	r.write(".assure/waivers.yaml", unbuiltWaivers)
	r.git("add", "-A")
	r.git("commit", "-qm", "waivers")
	return r
}

func readReport(t *testing.T, r repo) (app.EvalReport, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.root, app.ReportFile))
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.Report, data); err != nil || len(vs) > 0 {
		t.Fatalf("report invalid: %v %v\n%s", err, vs, data)
	}
	var rep app.EvalReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	return rep, data
}

func entry(rep app.EvalReport, objective, lang string) (app.Entry, bool) {
	for _, e := range rep.Objectives {
		if e.Objective == objective && e.Language == lang {
			return e, true
		}
	}
	return app.Entry{}, false
}

func noReport(t *testing.T, r repo) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(r.root, app.ReportFile)); err == nil {
		t.Fatal("report written on a usage or load error")
	}
}

func TestEvaluateWithoutRefExitsTwo(t *testing.T) {
	r := newRepo(t, levelB)
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	if code, _, stderr := assure("evaluate"); code != 2 || !strings.Contains(stderr, "--changed-from") {
		t.Fatalf("got %d %q", code, stderr)
	}
	noReport(t, r)
}

func TestEvaluateInvalidWaiversExitTwoNamingLocation(t *testing.T) {
	r := newRepo(t, levelB)
	r.write(".assure/waivers.yaml", "- objective: CODE-COMPLEXITY\n  scope: '**'\n  rationale: short\n  approver: owner\n  expires: 2099-01-01\n")
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	code, _, stderr := assure("evaluate", "--changed-from", "HEAD")
	if code != 2 || !strings.Contains(stderr, ".assure/waivers.yaml: line 3") || !strings.Contains(stderr, "/0/rationale") {
		t.Fatalf("got %d %q", code, stderr)
	}
	noReport(t, r)
}

func TestEvaluateNothingChangedExitsZeroWithEmptyReport(t *testing.T) {
	r := goRepo(t)
	code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD")
	if code != 0 {
		t.Fatalf("got %d\n%s\n%s", code, stdout, stderr)
	}
	if rep, _ := readReport(t, r); rep.ChangedFiles != 0 || len(rep.Objectives) != 0 {
		t.Fatalf("nothing changed, report lists %d files, %d objectives", rep.ChangedFiles, len(rep.Objectives))
	}
}

func TestEvaluateUnproducedObjectiveFailsWithoutWaiver(t *testing.T) {
	r := reviewedGoRepo(t)
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return b + a }\n")
	if code, _, _ := assure("evaluate", "--changed-from", "HEAD"); code != 1 {
		t.Fatalf("exit %d, want 1: missing evidence must block", code)
	}
	rep, _ := readReport(t, r)
	e, ok := entry(rep, "VER-TRACE-REQ", "")
	if !ok || e.Status != "advisory-fail" || len(e.Details) != 1 || e.Details[0] != "no adapter lists VER-TRACE-REQ" {
		t.Fatalf("VER-TRACE-REQ (advisory at B): %+v %v", e, ok)
	}
	ind, ok := entry(rep, "IND-VERIFIER-DISTINCT", "")
	if !ok || ind.Status != "fail" || len(ind.Details) != 1 || !strings.HasPrefix(ind.Details[0], "p/p.go: gap") {
		t.Fatalf("IND-VERIFIER-DISTINCT must be decided from provenance, not reported as unlisted: %+v %v", ind, ok)
	}
}

func TestEvaluateCleanChangePassesWithWaivers(t *testing.T) {
	r := waivedGoRepo(t)
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return b + a }\n")
	code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD")
	if code != 0 {
		t.Fatalf("got %d\n%s\n%s", code, stdout, stderr)
	}
	rep, _ := readReport(t, r)
	for _, id := range []string{"VER-TESTS-PASS", "CODE-ZERO-WARNINGS", "CODE-CHECK-RETURNS", "CODE-RESOURCE-BOUNDS", "CODE-COMPLEXITY", "CODE-NO-UNSAFE"} {
		if e, ok := entry(rep, id, "go"); !ok || e.Status != "pass" {
			t.Errorf("%s: %+v %v", id, e, ok)
		}
	}
	if e, _ := entry(rep, "IND-VERIFIER-DISTINCT", ""); e.Status != "waived" || len(e.Waivers) != 1 {
		t.Errorf("IND-VERIFIER-DISTINCT: %+v", e)
	}
	if rep.ToolVersions["go"]["golangci-lint"] == "" {
		t.Errorf("tool_versions: %v", rep.ToolVersions)
	}
}

func TestEvaluateLintFindingBlocks(t *testing.T) {
	r := waivedGoRepo(t)
	r.write("p/p.go", "package p\n\nimport \"fmt\"\n\nfunc Add(a, b int) int { fmt.Printf(\"%d\", \"x\"); return a + b }\n")
	code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD")
	if code != 1 {
		t.Fatalf("got %d\n%s\n%s", code, stdout, stderr)
	}
	rep, _ := readReport(t, r)
	e, _ := entry(rep, "CODE-ZERO-WARNINGS", "go")
	if e.Status != "fail" || len(e.Details) == 0 || !strings.HasPrefix(e.Details[0], "p/p.go: ") {
		t.Fatalf("CODE-ZERO-WARNINGS: %+v", e)
	}
	if !strings.Contains(stdout, "| CODE-ZERO-WARNINGS | go | fail |") {
		t.Fatalf("summary lacks the failing row:\n%s", stdout)
	}
}

func TestEvaluateReportIsReproducible(t *testing.T) {
	r := waivedGoRepo(t)
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int {\n\tif a > 0 {\n\t\treturn a + b\n\t}\n\treturn b + a\n}\n")
	var runs [2][]byte
	var outs [2]string
	for i := range runs {
		code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD", "--date", "2026-01-02")
		if code != 0 {
			t.Fatalf("run %d: %d\n%s\n%s", i, code, stdout, stderr)
		}
		_, runs[i] = readReport(t, r)
		outs[i] = stdout
	}
	if !bytes.Equal(runs[0], runs[1]) || outs[0] != outs[1] {
		t.Fatalf("reruns differ:\n%s\n---\n%s", runs[0], runs[1])
	}
	rep, _ := readReport(t, r)
	if rep.Date != "2026-01-02" {
		t.Fatalf("date %q", rep.Date)
	}
	if e, _ := entry(rep, "CODE-COMPLEXITY", "go"); e.UnderThreshold != 1 {
		t.Fatalf("CODE-COMPLEXITY under_threshold %d, want 1 (one function in the changed file)", e.UnderThreshold)
	}
}

func TestEvaluateExpiredWaiverBlocksEvenWithNothingChanged(t *testing.T) {
	r := goRepo(t)
	r.write(".assure/waivers.yaml", "- {objective: VER-FAIL-ON-BASE, scope: 'nowhere/**', rationale: fail-on-base runner arrives in M3b, approver: owner, expires: 2026-01-01}\n- {objective: VER-TEST-BUDGET, scope: 'nowhere/**', rationale: test budget check arrives in M3b, approver: owner, expires: 2026-01-02}\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "waivers")
	code, stdout, _ := assure("evaluate", "--changed-from", "HEAD", "--date", "2026-01-02")
	if code != 1 || !strings.Contains(stdout, "- VER-FAIL-ON-BASE, scope `nowhere/**`") || strings.Contains(stdout, "- VER-TEST-BUDGET") {
		t.Fatalf("got %d\n%s", code, stdout)
	}
	if rep, _ := readReport(t, r); len(rep.ExpiredWaivers) != 1 || rep.ExpiredWaivers[0].Expires != "2026-01-01" {
		t.Fatalf("expired_waivers: %+v", rep.ExpiredWaivers)
	}
}
