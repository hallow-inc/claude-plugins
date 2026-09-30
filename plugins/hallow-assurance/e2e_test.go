package hallowassurance_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func buildBinaries(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for name, pkg := range map[string]string{"assure": "./cmd/assure", "assure-adapter-go": "./adapters/go/assure-adapter-go"} {
		if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(bin, name), pkg).CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", name, err, out)
		}
	}
	return bin
}

func TestContextThenGuardEndToEnd(t *testing.T) {
	bin := buildBinaries(t)
	root := t.TempDir()
	manifest := "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: C\ncomponents:\n  - {path: 'internal/chat/**', level: B}\n"
	if err := os.WriteFile(filepath.Join(root, "assurance.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	assure := func(args ...string) (string, int) {
		cmd := exec.CommandContext(t.Context(), filepath.Join(bin, "assure"), args...)
		cmd.Dir, cmd.Env = root, env
		out, err := cmd.Output()
		code := 0
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	if out, code := assure("context"); code != 0 || !strings.Contains(out, "internal/chat/**  level B") {
		t.Fatalf("context exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".assure/state/adapters.json")); err != nil {
		t.Fatalf("context did not write the describe cache: %v", err)
	}
	test := filepath.Join(root, "internal/chat/stream_test.go")
	cache := filepath.Join(root, ".assure/state/adapters.json")
	if out, code := assure("guard", test); code != 1 || !strings.Contains(out, "hallow-assurance:verifier") {
		t.Errorf("main thread editing a level-B test: exit %d %q", code, out)
	}
	if out, code := assure("guard", "--agent-type", "hallow-assurance:verifier", test); code != 0 {
		t.Errorf("verifier editing a level-B test: exit %d %q", code, out)
	}
	for _, agent := range []string{"", "hallow-assurance:verifier"} {
		args := []string{"guard"}
		if agent != "" {
			args = append(args, "--agent-type", agent)
		}
		if out, code := assure(append(args, cache)...); code != 1 {
			t.Errorf("%q editing the describe cache: exit %d %q", agent, code, out)
		}
	}
}

func TestAdapterClassifyAgreesWithItsDescribeGlobs(t *testing.T) {
	bin := buildBinaries(t)
	adapter := filepath.Join(bin, "assure-adapter-go")
	out, err := exec.CommandContext(t.Context(), adapter, "describe").Output()
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Claims   []string            `json:"claims"`
		Patterns map[string][]string `json:"patterns"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	roles, err := core.NewRoles(map[string]core.RolePatterns{"go": {Claims: d.Claims, Patterns: d.Patterns}})
	if err != nil {
		t.Fatal(err)
	}
	seg := rapid.SampledFrom([]string{"a", "internal", "testdata", "fuzz", "FuzzX", "cmd"})
	name := rapid.SampledFrom([]string{"x.go", "x_test.go", "_test.go", ".go", "go.mod", "go.sum", "go.work", "go.work.sum", "README.md", "seed", "x.gox"})
	empty := t.TempDir()
	ctx := t.Context()
	rapid.Check(t, func(t *rapid.T) {
		var paths []string
		for range rapid.IntRange(1, 20).Draw(t, "n") {
			paths = append(paths, strings.Join(append(rapid.SliceOfN(seg, 0, 4).Draw(t, "dirs"), name.Draw(t, "name")), "/"))
		}
		cmd := exec.CommandContext(ctx, adapter, append([]string{"classify"}, paths...)...)
		cmd.Dir = empty
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Files []struct{ Path, Role string } `json:"files"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range resp.Files {
			got[f.Path] = f.Role
		}
		for _, p := range paths {
			want, err := roles.Of(p)
			if err != nil {
				t.Fatal(err)
			}
			g, ok := got[p]
			if !ok {
				g = string(core.Unclassified)
			}
			if g != string(want) {
				t.Fatalf("%s: classify says %s, describe globs say %s", p, g, want)
			}
		}
	})
}
