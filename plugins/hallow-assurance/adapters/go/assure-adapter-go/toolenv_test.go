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
	golangciDefaultInstall = `curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.13.2`
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
	return resp
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
	resp := toolsIn(t, root)
	if resp.Environment != nil || !reflect.DeepEqual(resp.Tools, want) {
		t.Fatalf("tools = %+v env %+v, want %+v; pinned_by is relative to the git top-level, so a pin above the manifest directory still names its file, and the installed 2.14.0 is not tools' concern", resp.Tools, resp.Environment, want)
	}
}

func TestUntrackedMiseOverrideFails(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"mise.local.toml": "[tools]\ngolangci-lint = \"2.13.2\"\n", "a/a.go": "package a\n\nvar X = 1\n"})
	narrowPath(t, []string{"git", "go"}, map[string]string{
		"mise": miseStub(t, dir, map[string][]miseEntry{"golangci-lint": pinEntry("2.13.2", filepath.Join(dir, "mise.local.toml"))}),
	})
	resp := toolsIn(t, dir)
	if resp.Environment == nil || len(resp.Tools) != 0 || !strings.Contains(resp.Environment.Message, "mise.local.toml") || !strings.Contains(resp.Environment.Message, "CI would not see it") {
		t.Fatalf("tools = %+v; an untracked override would make local and CI resolve different versions", resp)
	}
	out := filepath.Join(t.TempDir(), "ev")
	r, stderr, code := runIn(t, dir, "run", "CODE-ZERO-WARNINGS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	environmentIn(t, r, out, "mise.local.toml", "CI would not see it")
}

func TestTrackedConfigWithoutMiseFails(t *testing.T) {
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
	if resp := toolsIn(t, dir); resp.Environment != nil {
		t.Fatalf("untracked mise.toml without mise: %+v; only a config CI would read forces mise", resp.Environment)
	}
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
	environmentIn(t, resp, out, "golangci-lint", "2.14.0", "2.13.2", "default", golangciDefaultInstall)
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
	for _, m := range []string{"mise.toml", "golangci-lint", `"latest"`, `"golangci-lint" = "2.13.2"`} {
		if !strings.Contains(resp.Environment.Message, m) {
			t.Errorf("environment %q does not mention %q", resp.Environment.Message, m)
		}
	}
}
