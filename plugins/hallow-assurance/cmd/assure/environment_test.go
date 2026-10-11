package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cannedAdapters(t *testing.T, files map[string]map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	for lang := range files {
		d := filepath.Join(bin, lang+".d")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range files[lang] {
			if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		script := `#!/bin/sh
d='` + d + `'
case "$1" in
describe|tools) cat "$d/$1.json" ;;
run) mkdir -p "$6"; if [ -f "$d/$2.ev" ]; then cp "$d/$2.ev" "$6/ev"; fi; cat "$d/$2.json" ;;
*) echo "unexpected $*" >&2; exit 1 ;;
esac
`
		if err := os.WriteFile(filepath.Join(bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return bin
}

const envMessage = "golangci-lint 2.13.2 is pinned by mise.toml but is not on PATH"

var envWaivers = func() string {
	var b strings.Builder
	for _, id := range []string{"CODE-RESOURCE-BOUNDS", "CODE-CHECK-RETURNS", "CODE-COMPLEXITY", "CODE-NO-UNSAFE",
		"VER-FAIL-ON-BASE", "VER-TEST-BUDGET", "VER-ROBUST-FUZZ", "VER-COVERAGE-RESOLUTION"} {
		b.WriteString("- {objective: " + id + ", scope: '**', rationale: the canned adapter in this test produces no evidence, approver: owner, expires: 2099-01-01}\n")
	}
	return b.String()
}()

func envRepo(t *testing.T, envObjective string) repo {
	t.Helper()
	r := newRepo(t, xxManifest)
	sarif := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"golangci-lint"}},"results":[]}]}`
	junit := `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`
	xx := map[string]string{
		"describe.json":             `{"protocol":1,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true},"CODE-ZERO-WARNINGS":{"tool":"golangci-lint","fast":true},"VER-MUTATION-CHANGED":{"tool":"gremlins","fast":false}}}`,
		"VER-TESTS-PASS.json":       `{"protocol":1,"evidence":[{"type":"test.junit","path":"ev"}],"tool_versions":{"go":"go1.26.7","gotestsum":"1.12.0"},"pinned_by":{"gotestsum":"default"}}`,
		"VER-TESTS-PASS.ev":         junit,
		"CODE-ZERO-WARNINGS.json":   `{"protocol":1,"evidence":[{"type":"lint.sarif","path":"ev"}],"tool_versions":{"go":"go1.26.7","golangci-lint":"2.13.2"},"pinned_by":{"golangci-lint":"mise.toml"}}`,
		"CODE-ZERO-WARNINGS.ev":     sarif,
		"VER-MUTATION-CHANGED.json": `{"protocol":1,"evidence":[],"tool_versions":{"go":"go1.26.7","gremlins":"0.6.0"},"pinned_by":{"gremlins":"default"}}`,
	}
	xx[envObjective+".json"] = `{"protocol":1,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + envMessage + `"}}`
	delete(xx, envObjective+".ev")
	cannedAdapters(t, map[string]map[string]string{"xx": xx})
	r.write(".gitignore", ".assure/state/\n")
	r.write(".assure/waivers.yaml", envWaivers)
	r.write("p/x.xx", "one\n")
	r.git("init", "-q", "-b", "main")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	r.git("checkout", "-qb", "pr")
	r.write("p/x.xx", "two\n")
	return r
}

func TestEvaluateEnvironmentFailsAndPinOriginsAreRecorded(t *testing.T) {
	r := envRepo(t, "VER-MUTATION-CHANGED")
	var reports [2][]byte
	for i := range reports {
		code, stdout, stderr := assure("evaluate", "--changed-from", "main", "--date", "2026-10-01")
		if code != 1 {
			t.Fatalf("run %d: exit %d, want 1: a tool the adapter could not get must fail closed\n%s\n%s", i, code, stdout, stderr)
		}
		_, reports[i] = readReport(t, r)
	}
	if !bytes.Equal(reports[0], reports[1]) {
		t.Fatalf("reports differ across identical runs:\n%s\n---\n%s", reports[0], reports[1])
	}
	rep, _ := readReport(t, r)
	if e, ok := entry(rep, "VER-MUTATION-CHANGED", "xx"); !ok || e.Status != "fail" || !detailWith(e, envMessage) {
		t.Fatalf("VER-MUTATION-CHANGED %+v; want fail with the environment message as detail", e)
	}
	for _, id := range []string{"VER-TESTS-PASS", "CODE-ZERO-WARNINGS"} {
		if e, _ := entry(rep, id, "xx"); e.Status != "pass" {
			t.Fatalf("%s %+v; an environment failure in one objective must not spill into another", id, e)
		}
	}
	want := map[string]string{"golangci-lint": "mise.toml", "gotestsum": "default"}
	if !maps.Equal(rep.PinnedBy["xx"], want) {
		t.Fatalf("pinned_by %v, want %v merged from every run, with no go key though tool_versions has one", rep.PinnedBy, want)
	}
	if rep.ToolVersions["xx"]["go"] != "go1.26.7" {
		t.Fatalf("tool_versions %v", rep.ToolVersions)
	}
}

func TestFastCheckEnvironmentFailsWithItsMessage(t *testing.T) {
	envRepo(t, "CODE-ZERO-WARNINGS")
	code, stdout, stderr := assure("check", "--fast")
	if code != 1 || !strings.Contains(stdout, "fail\tCODE-ZERO-WARNINGS\txx\n") || !strings.Contains(stdout, "CODE-ZERO-WARNINGS (xx): fail\n  - "+envMessage+"\n") {
		t.Fatalf("exit %d; want CODE-ZERO-WARNINGS to fail with the environment message as its detail\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "pass\tVER-TESTS-PASS\txx\n") {
		t.Fatalf("VER-TESTS-PASS should pass:\n%s", stdout)
	}
}
