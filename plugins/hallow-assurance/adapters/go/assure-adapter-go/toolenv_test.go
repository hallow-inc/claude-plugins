package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	golangciDefaultInstall = `curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.15.0`
	gremlinsDefaultInstall = "go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0"
)

func narrowPath(t *testing.T, real []string, stubs map[string]string) {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range real {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func miseStub(t *testing.T, root string, ls map[string][]miseEntry) string {
	t.Helper()
	data, err := json.Marshal(ls)
	if err != nil {
		t.Fatal(err)
	}
	return `[ "$*" = "ls --json --local" ] || { echo "unexpected mise args: $*" >&2; exit 3; }
[ "$(pwd -P)" = "` + root + `" ] || { echo "mise ran in $(pwd -P)" >&2; exit 4; }
printf '%s\n' '` + string(data) + `'
`
}

func pinEntry(pin, file string) []miseEntry {
	return []miseEntry{{Version: pin, RequestedVersion: pin, Source: miseSource{Type: "mise.toml", Path: file}}}
}

func toolsIn(t *testing.T, root string) toolsResponse {
	t.Helper()
	resp, _ := toolsRawIn(t, root)
	return resp
}

func toolsRawIn(t *testing.T, root string) (toolsResponse, []byte) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"tools", "--root", root}, &out, &errb); code != 0 {
		t.Fatalf("tools exited %d: %s; tools must exit 0 whenever it produced a response", code, errb.String())
	}
	if vs, err := schemas.Validate(schemas.AdapterTools, out.Bytes()); err != nil || len(vs) > 0 {
		t.Fatalf("tools response invalid: %v %v\n%s", err, vs, out.Bytes())
	}
	var resp toolsResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Protocol != 2 {
		t.Errorf("tools response protocol %d, want 2; entries gained the optional untracked flag, and an assure that predates it must refuse the response as a protocol mismatch\n%s", resp.Protocol, out.Bytes())
	}
	return resp, out.Bytes()
}

func toolsUntracked(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var resp struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range resp.Tools {
		var name string
		if err := json.Unmarshal(e["name"], &name); err != nil {
			t.Fatal(err)
		}
		if u, ok := e["untracked"]; ok {
			out[name] = string(u)
		}
	}
	return out
}

func untrackedConfigWithoutMise(t *testing.T, root, config string) {
	t.Helper()
	resp := toolsIn(t, root)
	if resp.Environment == nil || len(resp.Tools) != 0 {
		t.Fatalf("untracked %s, no mise: tools = %+v; mise still reads an untracked config, so falling back to the compiled default would resolve a different version than the developer pinned", config, resp)
	}
	for _, m := range []string{"install mise", config, "golangci-lint", "gremlins"} {
		if !strings.Contains(resp.Environment.Message, m) {
			t.Errorf("untracked %s: environment %q does not mention %q", config, resp.Environment.Message, m)
		}
	}
}

func environmentIn(t *testing.T, resp runResponse, out string, mention ...string) {
	t.Helper()
	if resp.Environment == nil {
		t.Fatalf("response %+v has no environment", resp)
	}
	for _, m := range mention {
		if !strings.Contains(resp.Environment.Message, m) {
			t.Errorf("environment %q does not mention %q", resp.Environment.Message, m)
		}
	}
	if entries, _ := os.ReadDir(out); len(entries) > 0 {
		t.Errorf("evidence written to %s despite environment: %v", out, entries)
	}
}

func TestToolsReadsTrackedAncestorPinAndDefaultsTheRest(t *testing.T) {
	top := committedModule(t, map[string]string{"mise.toml": "[tools]\ngolangci-lint = \"2.13.2\"\n", "mod/a.go": "package a\n"})
	root := filepath.Join(top, "mod")
	narrowPath(t, []string{"git"}, map[string]string{
		"mise":          miseStub(t, root, map[string][]miseEntry{"golangci-lint": pinEntry("2.13.2", filepath.Join(top, "mise.toml"))}),
		"golangci-lint": "echo 2.14.0\n",
	})
	want := []toolEntry{
		{Name: "golangci-lint", Version: "2.13.2", PinnedBy: "mise.toml", Install: "mise install golangci-lint@2.13.2"},
		{Name: "gremlins", Version: "0.6.0", PinnedBy: "default", Install: gremlinsDefaultInstall},
	}
	resp, raw := toolsRawIn(t, root)
	if resp.Environment != nil || !reflect.DeepEqual(resp.Tools, want) {
		t.Fatalf("tools = %+v env %+v, want %+v; pinned_by is relative to the git top-level, so a pin above the manifest directory still names its file, and the installed 2.14.0 is not tools' concern", resp.Tools, resp.Environment, want)
	}
	if u := toolsUntracked(t, raw); len(u) != 0 {
		t.Fatalf("tools entries carry untracked %v for a tracked pin and a default; absent means tracked, and a spurious mark fails the objective under authority: ci", u)
	}
}

func TestTrackedPinCarriesNoUntrackedInRun(t *testing.T) {
	dir := committedModule(t, map[string]string{"mise.toml": "[tools]\ngolangci-lint = \"2.13.2\"\n", "a/a.go": "package a\n"})
	narrowPath(t, []string{"git", "go"}, map[string]string{
		"mise":          miseStub(t, dir, map[string][]miseEntry{"golangci-lint": pinEntry("2.13.2", filepath.Join(dir, "mise.toml"))}),
		"golangci-lint": `[ "$*" = "version --short" ] || { echo "linted: $*" >&2; exit 9; }` + "\necho 2.13.2\n",
	})
	resp, raw, stderr, code := runRawIn(t, dir, "run", "CODE-ZERO-WARNINGS", "--changed-from", "HEAD", "--out", filepath.Join(t.TempDir(), "ev"))
	if code != 0 || resp.Environment != nil || resp.PinnedBy["golangci-lint"] != "mise.toml" {
		t.Fatalf("exit %d %s, response %+v; a tracked exact pin resolves", code, stderr, resp)
	}
	if u, ok := runUntracked(t, raw); ok {
		t.Fatalf("run response has untracked %v for a tracked pin; absent means every pin is tracked\n%s", u, raw)
	}
}

func TestUntrackedMiseOverrideResolvesMarkedUntracked(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"mise.local.toml": "[tools]\ngolangci-lint = \"2.13.2\"\n"})
	narrowPath(t, []string{"git", "go"}, map[string]string{
		"mise":          miseStub(t, dir, map[string][]miseEntry{"golangci-lint": pinEntry("2.13.2", filepath.Join(dir, "mise.local.toml"))}),
		"golangci-lint": `[ "$*" = "version --short" ] || { echo "linted: $*" >&2; exit 9; }` + "\necho 2.13.2\n",
	})
	resp, raw := toolsRawIn(t, dir)
	want := []toolEntry{
		{Name: "golangci-lint", Version: "2.13.2", PinnedBy: "mise.local.toml", Install: "mise install golangci-lint@2.13.2"},
		{Name: "gremlins", Version: "0.6.0", PinnedBy: "default", Install: gremlinsDefaultInstall},
	}
	if resp.Environment != nil || !reflect.DeepEqual(resp.Tools, want) {
		t.Fatalf("tools = %+v env %+v, want %+v; without authority: ci the developer's untracked pin is honoured and reported, not refused", resp.Tools, resp.Environment, want)
	}
	if u := toolsUntracked(t, raw); !reflect.DeepEqual(u, map[string]string{"golangci-lint": "true"}) {
		t.Fatalf("tools untracked marks = %v, want only golangci-lint: true; the evaluator fails exactly the marked pins under authority: ci, so the default-pinned gremlins must stay unmarked\n%s", u, raw)
	}
	r, rawRun, stderr, code := runRawIn(t, dir, "run", "CODE-ZERO-WARNINGS", "--changed-from", "HEAD", "--out", filepath.Join(t.TempDir(), "ev"))
	if code != 0 || r.Environment != nil || r.ToolVersions["golangci-lint"] != "2.13.2" || r.PinnedBy["golangci-lint"] != "mise.local.toml" {
		t.Fatalf("exit %d %s, response %+v; the run resolves the untracked pin and records where it came from", code, stderr, r)
	}
	if u, ok := runUntracked(t, rawRun); !ok || !reflect.DeepEqual(u, []string{"golangci-lint"}) {
		t.Fatalf("run untracked = %v (present %v), want [golangci-lint]; the evaluator cannot tell an untracked pin from a tracked one without it\n%s", u, ok, rawRun)
	}
}

func TestUntrackedLoosePinStillFails(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"mise.local.toml": "[tools]\ngolangci-lint = \"latest\"\n"})
	narrowPath(t, []string{"git"}, map[string]string{
		"mise": miseStub(t, dir, map[string][]miseEntry{"golangci-lint": pinEntry("latest", filepath.Join(dir, "mise.local.toml"))}),
	})
	resp := toolsIn(t, dir)
	if resp.Environment == nil || len(resp.Tools) != 0 {
		t.Fatalf("tools = %+v; an untracked pin is honoured, but latest still resolves differently on each run", resp)
	}
	for _, m := range []string{"mise.local.toml", "golangci-lint", `"latest"`} {
		if !strings.Contains(resp.Environment.Message, m) {
			t.Errorf("environment %q does not mention %q", resp.Environment.Message, m)
		}
	}
	if strings.Contains(resp.Environment.Message, "CI would not see it") {
		t.Errorf("environment %q still refuses the pin for being untracked; the failure must be the loose pin", resp.Environment.Message)
	}
}

func TestMiseConfigWithoutMiseFails(t *testing.T) {
	tops := map[string]string{}
	for _, name := range miseConfigNames {
		top := committedModule(t, map[string]string{"mod/a.go": "package a\n"})
		writeFiles(t, top, map[string]string{name: "[tools]\ngo = \"1.26\"\n"})
		git(t, top, "add", "-f", name)
		git(t, top, "commit", "-qm", "pin")
		tops[name] = top
	}
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"mise.toml": "[tools]\n"})
	excluded := committedModule(t, map[string]string{"mod/a.go": "package a\n"})
	writeFiles(t, excluded, map[string]string{".config/mise.toml": "[tools]\n", ".git/info/exclude": ".config/\n"})
	narrowPath(t, []string{"git"}, nil)
	for name, top := range tops {
		resp := toolsIn(t, filepath.Join(top, "mod"))
		if resp.Environment == nil || len(resp.Tools) != 0 {
			t.Fatalf("%s tracked in an ancestor, no mise: tools = %+v; without mise the adapter cannot read the pins CI reads, so it must not guess the default", name, resp)
		}
		for _, m := range []string{"install mise", name, "golangci-lint", "gremlins"} {
			if !strings.Contains(resp.Environment.Message, m) {
				t.Errorf("%s: environment %q does not mention %q", name, resp.Environment.Message, m)
			}
		}
	}
	untrackedConfigWithoutMise(t, dir, "mise.toml")
	untrackedConfigWithoutMise(t, filepath.Join(excluded, "mod"), ".config/mise.toml")
}

func TestFoundVersionMustEqualPin(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"a/a.go": "package a\n\nvar X = 1\n"})
	narrowPath(t, []string{"git", "go"}, map[string]string{
		"golangci-lint": `[ "$*" = "version --short" ] || { echo "linted: $*" >&2; exit 9; }` + "\necho 2.14.0\n",
	})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "CODE-ZERO-WARNINGS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s; a wrong installed version is an environment problem, not an adapter error", code, stderr)
	}
	environmentIn(t, resp, out, "golangci-lint", "2.14.0", "2.15.0", "default", golangciDefaultInstall)
}

func TestUnusedToolNotChecked(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"})
	narrowPath(t, []string{"git", "go"}, nil)
	resp, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", filepath.Join(t.TempDir(), "ev"))
	if code != 0 || resp.Environment != nil || len(resp.Evidence) != 1 || resp.Evidence[0].Type != "test.junit" {
		t.Fatalf("exit %d %s, response %+v; a missing gremlins must not block the tests objective", code, stderr, resp)
	}
	if resp.ToolVersions["go"] == "" || len(resp.PinnedBy) != 0 {
		t.Fatalf("tool_versions %v pinned_by %v; go is recorded but never pinned (go.mod selects it)", resp.ToolVersions, resp.PinnedBy)
	}
}

func TestToolsLoosePinExitsZeroWithEnvironment(t *testing.T) {
	dir := committedModule(t, map[string]string{"mise.toml": "[tools]\ngolangci-lint = \"latest\"\n"})
	narrowPath(t, []string{"git"}, map[string]string{
		"mise": miseStub(t, dir, map[string][]miseEntry{"golangci-lint": pinEntry("latest", filepath.Join(dir, "mise.toml"))}),
	})
	resp := toolsIn(t, dir)
	if resp.Environment == nil || len(resp.Tools) != 0 {
		t.Fatalf("tools = %+v; latest resolves differently on each machine", resp)
	}
	for _, m := range []string{"mise.toml", "golangci-lint", `"latest"`, `"golangci-lint" = "2.15.0"`} {
		if !strings.Contains(resp.Environment.Message, m) {
			t.Errorf("environment %q does not mention %q", resp.Environment.Message, m)
		}
	}
}
