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

const envMessage = "gremlins 0.6.0 is pinned by mise.toml but is not on PATH"

var envWaivers = func() string {
	var b strings.Builder
	for _, id := range []string{"VER-FAIL-ON-BASE", "VER-ROBUST-FUZZ", "VER-COVERAGE-RESOLUTION"} {
		b.WriteString("- {objective: " + id + ", scope: '**', rationale: the canned adapter in this test produces no evidence, approver: owner, expires: 2099-01-01}\n")
	}
	return b.String()
}()

const killedXXMutant = `{"schemaVersion":"1","thresholds":{"high":80,"low":60},"files":{"p/x.xx":{"language":"xx","source":"two\n","mutants":[{"id":"p/x.xx:1:1:M","mutatorName":"M","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":1}},"status":"Killed"}]}}}`

func mutationResponse(pin string, untracked bool) string {
	u := ""
	if untracked {
		u = `,"untracked":["gremlins"]`
	}
	return `{"protocol":2,"evidence":[{"type":"mutation.report","path":"ev"}],"tool_versions":{"go":"go1.26.7","gremlins":"0.6.0"},"pinned_by":{"gremlins":"` + pin + `"}` + u + `}`
}

func passingXXResponses() map[string]string {
	junit := `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`
	return map[string]string{
		"describe.json":             `{"protocol":2,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"t","fast":true},"VER-MUTATION-CHANGED":{"tool":"gremlins","fast":false}}}`,
		"VER-TESTS-PASS.json":       `{"protocol":2,"evidence":[{"type":"test.junit","path":"ev"}],"tool_versions":{"go":"go1.26.7","gotestsum":"1.12.0"},"pinned_by":{"gotestsum":"default"}}`,
		"VER-TESTS-PASS.ev":         junit,
		"VER-MUTATION-CHANGED.json": mutationResponse("mise.toml", false),
		"VER-MUTATION-CHANGED.ev":   killedXXMutant,
	}
}

func envRepo(t *testing.T, envObjective string) repo {
	t.Helper()
	xx := passingXXResponses()
	xx[envObjective+".json"] = `{"protocol":2,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + envMessage + `"}}`
	delete(xx, envObjective+".ev")
	return cannedXXRepo(t, xxManifest, xx)
}

func cannedXXRepo(t *testing.T, manifest string, xx map[string]string) repo {
	t.Helper()
	r := newRepo(t, manifest)
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

func TestEvaluatePinOriginsAreMergedFromEveryRun(t *testing.T) {
	r := cannedXXRepo(t, xxManifest, passingXXResponses())
	code, stdout, stderr := assure("evaluate", "--changed-from", "main", "--date", "2026-10-01")
	if code == 2 {
		t.Fatalf("exit 2:\n%s\n%s", stdout, stderr)
	}
	rep, _ := readReport(t, r)
	if e, _ := entry(rep, "VER-MUTATION-CHANGED", "xx"); e.Status != "pass" {
		t.Fatalf("VER-MUTATION-CHANGED %+v; its only mutant was killed", e)
	}
	want := map[string]string{"gremlins": "mise.toml", "gotestsum": "default"}
	if !maps.Equal(rep.PinnedBy["xx"], want) {
		t.Fatalf("pinned_by %v, want %v merged from both runs, with no go key though tool_versions has one", rep.PinnedBy, want)
	}
	if rep.ToolVersions["xx"]["go"] != "go1.26.7" || rep.ToolVersions["xx"]["gremlins"] != "0.6.0" {
		t.Fatalf("tool_versions %v", rep.ToolVersions)
	}
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
	if e, _ := entry(rep, "VER-TESTS-PASS", "xx"); e.Status != "pass" {
		t.Fatalf("VER-TESTS-PASS %+v; an environment failure in one objective must not spill into another", e)
	}
	want := map[string]string{"gotestsum": "default"}
	if !maps.Equal(rep.PinnedBy["xx"], want) {
		t.Fatalf("pinned_by %v, want %v: the environment run reported no pins, and go is never a pin", rep.PinnedBy, want)
	}
	if rep.ToolVersions["xx"]["go"] != "go1.26.7" {
		t.Fatalf("tool_versions %v", rep.ToolVersions)
	}
}

func TestFastCheckWithoutTestEvidenceFails(t *testing.T) {
	for name, evidence := range map[string]string{
		"none":       `[]`,
		"lint.sarif": `[{"type":"lint.sarif","path":"ev"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			xx := passingXXResponses()
			xx["VER-TESTS-PASS.json"] = `{"protocol":2,"evidence":` + evidence + `,"tool_versions":{"go":"go1.26.7"},"pinned_by":{}}`
			cannedXXRepo(t, xxManifest, xx)
			code, stdout, stderr := assure("check", "--fast")
			want := "assure-adapter-xx run VER-TESTS-PASS produced no test.junit evidence"
			if code != 1 || !strings.Contains(stdout, "fail\tVER-TESTS-PASS\txx\n") || !strings.Contains(stdout, want) {
				t.Fatalf("exit %d; want VER-TESTS-PASS to fail closed with %q: a run that wrote no JUnit proved nothing, and a lint.sarif entry is no longer evidence for any objective\n%s\n%s", code, want, stdout, stderr)
			}
		})
	}
}

func TestFastCheckEnvironmentFailsWithItsMessage(t *testing.T) {
	envRepo(t, "VER-TESTS-PASS")
	code, stdout, stderr := assure("check", "--fast")
	if code != 1 || !strings.Contains(stdout, "fail\tVER-TESTS-PASS\txx\n") || !strings.Contains(stdout, "VER-TESTS-PASS (xx): fail\n  - "+envMessage+"\n") {
		t.Fatalf("exit %d; want VER-TESTS-PASS to fail with the environment message as its detail, since a run that could not get its tools proved nothing\n%s\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "VER-MUTATION-CHANGED") {
		t.Fatalf("the fast check ran VER-MUTATION-CHANGED, which the adapter does not mark fast:\n%s", stdout)
	}
}
