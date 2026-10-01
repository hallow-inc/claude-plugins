package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const inputsManifest = "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents:\n  - {path: 'p/**', level: B, inputs: true}\n"

const partialWaivers = `- {objective: IND-VERIFIER-DISTINCT, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: CFG-PROTECTED, scope: '**', rationale: provenance arrives in milestone M4, approver: owner, expires: 2099-01-01}
- {objective: VER-MUTATION-CHANGED, scope: '**', rationale: mutation runner arrives in M3b, approver: owner, expires: 2099-01-01}
- {objective: VER-FAIL-ON-BASE, scope: '**', rationale: fail-on-base runner arrives in M3b, approver: owner, expires: 2099-01-01}
`

const resolutionsYAML = `- {path: p/p.go, function: Fresh, resolution: missing-test, rationale: exercised only through the CLI in M5, approver: owner}
- {path: p/p.go, function: Removed, resolution: dead, rationale: the function was inlined away, approver: owner}
- {path: p/q.go, function: Gone, resolution: dead, rationale: the file was deleted with its feature, approver: owner}
`

func inputsRepo(t *testing.T) repo {
	t.Helper()
	path := toolPath(t, "go", "golangci-lint")
	r := newRepo(t, inputsManifest)
	t.Setenv("PATH", path)
	r.write(".gitignore", ".assure/state/\n")
	r.write(".assure/waivers.yaml", partialWaivers)
	r.write(".assure/coverage-resolutions.yaml", resolutionsYAML)
	r.write("go.mod", "module example.com/m\n\ngo 1.26\n")
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return a + b }\n")
	r.write("p/q.go", "package p\n\nfunc Sub(a, b int) int { return a - b }\n")
	r.write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 || Sub(3, 1) != 2 {\n\t\tt.Fatal(\"arith\")\n\t}\n}\n")
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	return r
}

func TestEvaluateDeletedInputsSourceDemandsNoFuzzEvidence(t *testing.T) {
	r := inputsRepo(t)
	if err := os.Remove(filepath.Join(r.root, "p/q.go")); err != nil {
		t.Fatal(err)
	}
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Fresh() int { return 1 }\n")
	r.write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"arith\")\n\t}\n}\n")
	assure("evaluate", "--changed-from", "HEAD", "--date", "2026-01-02")
	rep, _ := readReport(t, r)
	e, ok := entry(rep, "VER-ROBUST-FUZZ", "go")
	if !ok {
		t.Fatalf("no VER-ROBUST-FUZZ entry: %+v", rep.Objectives)
	}
	details := strings.Join(e.Details, "\n")
	if !strings.Contains(details, "p/p.go: no fuzz") {
		t.Fatalf("modified inputs source p/p.go is not held to fuzz evidence:\n%s", details)
	}
	if strings.Contains(details, "p/q.go") {
		t.Fatalf("deleted p/q.go is demanded fuzz evidence:\n%s", details)
	}
}

func TestEvaluateReportsAppliedAndRemovableResolutionsReproducibly(t *testing.T) {
	r := inputsRepo(t)
	if err := os.Remove(filepath.Join(r.root, "p/q.go")); err != nil {
		t.Fatal(err)
	}
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Fresh() int {\n\treturn 1\n}\n")
	r.write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"arith\")\n\t}\n}\n")
	var reports [2][]byte
	var outs [2]string
	for i := range reports {
		code, stdout, stderr := assure("evaluate", "--changed-from", "HEAD", "--date", "2026-01-02")
		if code == 2 {
			t.Fatalf("run %d: %d\n%s\n%s", i, code, stdout, stderr)
		}
		_, reports[i] = readReport(t, r)
		outs[i] = stdout
	}
	if !bytes.Equal(reports[0], reports[1]) || outs[0] != outs[1] {
		t.Fatalf("reruns differ:\n%s\n---\n%s", reports[0], reports[1])
	}
	rep, _ := readReport(t, r)
	e, ok := entry(rep, "VER-COVERAGE-RESOLUTION", "go")
	if !ok || !slices.EqualFunc(e.Resolutions, []core.Resolution{{Path: "p/p.go", Function: "Fresh"}}, func(a, b core.Resolution) bool { return a.Path == b.Path && a.Function == b.Function }) {
		t.Fatalf("applied resolutions %+v (entry found %v), want only p/p.go Fresh; details %v", e.Resolutions, ok, e.Details)
	}
	var removable []string
	for _, x := range rep.RemovableResolutions {
		removable = append(removable, x.Path+" "+x.Function)
	}
	if want := []string{"p/p.go Removed", "p/q.go Gone"}; !slices.Equal(removable, want) {
		t.Fatalf("removable %v, want %v: a resolution whose function left the LCOV record, and one whose file is gone", removable, want)
	}
	for _, h := range []string{"### Applied coverage resolutions", "### Removable coverage resolutions"} {
		if !strings.Contains(outs[0], h) {
			t.Fatalf("summary lacks %q:\n%s", h, outs[0])
		}
	}
}
