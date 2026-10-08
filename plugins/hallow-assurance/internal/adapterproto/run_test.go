package adapterproto

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestRunObjectiveFailureModesNameTheAdapter(t *testing.T) {
	cases := map[string]struct{ script, want string }{
		"timeout":           {"exec sleep 10", "killed after timeout of 1s"},
		"invalid response":  {`echo '{"protocol":1,"evidence":[]}'`, "violates the protocol"},
		"unknown objective": {`echo "objective \"$2\" is not implemented" >&2; exit 2`, `objective "VER-X" is not implemented`},
		"escaping path":     {`echo '{"protocol":1,"evidence":[{"type":"test.junit","path":"../x"}],"tool_versions":{"go":"1"},"pinned_by":{}}'`, "violates the protocol"},
		"unversioned pin":   {`echo '{"protocol":1,"evidence":[],"tool_versions":{"go":"go1.26.7"},"pinned_by":{"gremlins":"default"}}'`, `"gremlins" has no tool_versions entry`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fakeAdapter(t, "go", c.script)
			_, err := RunObjective(t.TempDir(), "go", "VER-X", "HEAD", "out", time.Second)
			var ae *Error
			if !errors.As(err, &ae) || ae.Adapter != "assure-adapter-go" || ae.Sub != "run" {
				t.Fatalf("want *Error naming assure-adapter-go run, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestRunEnvironmentIsAResultNotAnError(t *testing.T) {
	fakeAdapter(t, "go", `echo '{"protocol":1,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"golangci-lint is not on PATH"}}'`)
	r, err := RunObjective(t.TempDir(), "go", "CODE-ZERO-WARNINGS", "main", "ev", 5*time.Second)
	if err != nil {
		t.Fatalf("environment must reach check and evaluate as a typed result so they can classify it outside the agent's reach; got error %v", err)
	}
	if r.Environment == nil || r.Environment.Message != "golangci-lint is not on PATH" || len(r.Evidence) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func respondingAdapter(t *testing.T, script string) string {
	t.Helper()
	resp := filepath.Join(t.TempDir(), "response.json")
	fakeAdapter(t, "go", script+"\ncat '"+resp+"'")
	return resp
}

func TestProtocolMismatchNamesBothNumbersBeforeSchema(t *testing.T) {
	resp := respondingAdapter(t, "")
	dir := t.TempDir()
	exe, err := Resolve("go")
	if err != nil {
		t.Fatal(err)
	}
	subs := map[string]struct {
		want int64
		call func() error
	}{
		"describe": {1, func() error { _, _, err := RunDescribe(exe, dir, "go"); return err }},
		"classify": {0, func() error { _, err := Classify(dir, "go", []string{"a.go"}); return err }},
		"run": {1, func() error {
			_, err := RunObjective(dir, "go", "VER-TESTS-PASS", "main", "ev", time.Minute)
			return err
		}},
		"tools": {1, func() error { _, err := RunTools(dir, "go"); return err }},
	}
	rapid.Check(t, func(rt *rapid.T) {
		sub := rapid.SampledFrom(slices.Sorted(maps.Keys(subs))).Draw(rt, "sub")
		got := rapid.Int64Range(-3, 1<<40).Filter(func(n int64) bool { return n != subs[sub].want }).Draw(rt, "protocol")
		if err := os.WriteFile(resp, fmt.Appendf(nil, `{"protocol":%d}`, got), 0o644); err != nil {
			rt.Fatal(err)
		}
		err := subs[sub].call()
		var ae *Error
		if !errors.As(err, &ae) || ae.Adapter != "assure-adapter-go" || ae.Sub != sub {
			rt.Fatalf("want *Error naming assure-adapter-go %s, got %v", sub, err)
		}
		want := fmt.Sprintf("received protocol %d, expected %d", got, subs[sub].want)
		if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "same release") || strings.Contains(err.Error(), "violates the protocol") {
			rt.Fatalf("error %q must say %q and 'same release' instead of a schema violation, so a version skew is not mistaken for a broken adapter", err, want)
		}
	})
}

var toolGen = rapid.Custom(func(rt *rapid.T) Tool {
	return Tool{
		Name:     rapid.SampledFrom([]string{"golangci-lint", "gremlins", "go", "x"}).Draw(rt, "name"),
		Version:  rapid.StringMatching(`[0-9]\.[0-9]{1,2}\.[0-9]`).Draw(rt, "version"),
		PinnedBy: rapid.SampledFrom([]string{"default", "mise.toml", ".tool-versions"}).Draw(rt, "pinned_by"),
		Install:  rapid.StringMatching(`[a-z@./ -]{1,30}`).Draw(rt, "install"),
	}
})

func firstDuplicate(tools []Tool) string {
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] {
			return tool.Name
		}
		seen[tool.Name] = true
	}
	return ""
}

func TestRunToolsKeepsOrderAndRejectsDuplicateNames(t *testing.T) {
	resp := respondingAdapter(t, `[ "$1 $2 $3" = "tools --root $PWD" ] || { echo "args: $*" >&2; exit 1; }`)
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		tools := rapid.SliceOf(toolGen).Draw(rt, "tools")
		env := len(tools) == 0 && rapid.Bool().Draw(rt, "environment")
		body := map[string]any{"protocol": 1, "tools": tools}
		if env {
			body["environment"] = map[string]any{"message": "loose pin"}
		}
		data, err := json.Marshal(body)
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.WriteFile(resp, data, 0o644); err != nil {
			rt.Fatal(err)
		}
		dup := firstDuplicate(tools)
		got, err := RunTools(dir, "go")
		switch {
		case dup != "":
			if err == nil || !strings.Contains(err.Error(), "assure-adapter-go tools") || !strings.Contains(err.Error(), fmt.Sprintf("duplicate tool name %q", dup)) {
				rt.Fatalf("a duplicate %q would print two install lines for one tool; got %v", dup, err)
			}
		case err != nil:
			rt.Fatalf("valid response rejected: %v", err)
		case env != (got.Environment != nil) || (env && got.Environment.Message != "loose pin"):
			rt.Fatalf("environment %v not surfaced as a result: %+v", env, got)
		case !slices.Equal(got.Tools, tools):
			rt.Fatalf("tools reordered or altered: got %+v want %+v", got.Tools, tools)
		}
	})
}

func TestRunObjectivePassesProtocolArguments(t *testing.T) {
	fakeAdapter(t, "go", `[ "$1 $2 $3 $4 $5 $6" = "run VER-TESTS-PASS --changed-from main --out ev" ] || { echo "args: $*" >&2; exit 1; }
echo '{"protocol":1,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"go":"go1.26"},"pinned_by":{"go":"go.mod"}}'`)
	r, err := RunObjective(t.TempDir(), "go", "VER-TESTS-PASS", "main", "ev", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Evidence) != 1 || r.Evidence[0].Path != "junit.xml" || r.ToolVersions["go"] != "go1.26" || r.PinnedBy["go"] != "go.mod" {
		t.Fatalf("got %+v", r)
	}
}
