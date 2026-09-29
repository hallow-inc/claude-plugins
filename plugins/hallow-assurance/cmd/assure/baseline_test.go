package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const oldFinding = "package p\n\nfunc old() {}\n"

func TestBaselineRoundTripSuppressesOldFindingsOnly(t *testing.T) {
	r := waivedGoRepo(t)
	r.write("p/old.go", oldFinding)
	r.git("add", "-A")
	r.git("commit", "-qm", "old finding")
	code, stdout, stderr := assure("baseline")
	if code != 0 {
		t.Fatalf("baseline: %d\n%s\n%s", code, stdout, stderr)
	}
	data, err := os.ReadFile(filepath.Join(r.root, core.BaselineFile))
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.Baseline, data); err != nil || len(vs) > 0 {
		t.Fatalf("baseline invalid: %v %v\n%s", err, vs, data)
	}
	b, err := core.ParseBaseline(core.BaselineFile, data)
	if err != nil || len(b.Entries) == 0 || b.Entries[0].Path != "p/old.go" {
		t.Fatalf("baseline %+v %v", b.Entries, err)
	}
	r.write("p/old.go", "package p\n\n// moved down a line\nfunc old() {}\n")
	code, stdout, _ = assure("evaluate", "--changed-from", "HEAD")
	rep, _ := readReport(t, r)
	if e, _ := entry(rep, "CODE-ZERO-WARNINGS", "go"); code != 0 || e.Status != "pass" || e.Baselined == 0 {
		t.Fatalf("moved old finding not baselined: %d %+v\n%s", code, e, stdout)
	}
	r.write("p/new.go", strings.ReplaceAll(oldFinding, "old", "fresh"))
	code, _, _ = assure("evaluate", "--changed-from", "HEAD")
	rep, _ = readReport(t, r)
	if e, _ := entry(rep, "CODE-ZERO-WARNINGS", "go"); code != 1 || e.Status != "fail" || !strings.HasPrefix(e.Details[0], "p/new.go: ") {
		t.Fatalf("new finding hidden by baseline: %d %+v", code, e)
	}
}

func TestBaselineAdapterFailureLeavesFileUnchanged(t *testing.T) {
	r := goRepo(t)
	before := []byte("{\"version\": 0, \"entries\": []}\n")
	r.write(core.BaselineFile, string(before))
	t.Setenv("PATH", toolPath(t, "go"))
	code, _, stderr := assure("baseline")
	if code != 2 || !strings.Contains(stderr, "not written") {
		t.Fatalf("got %d %q", code, stderr)
	}
	after, err := os.ReadFile(filepath.Join(r.root, core.BaselineFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("baseline changed on failure: %v\n%s", err, after)
	}
}
