package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

const integrationVar = "ASSURE_TS_INTEGRATION"

var nodeFixtureDir, _ = filepath.Abs(filepath.Join("testdata", "node"))

var (
	npmOnce    sync.Once
	npmModules string
	npmErr     error
)

// installedModules runs npm ci once per process into a directory keyed by
// the fixture's contents, reused across runs and packages, and returns its
// node_modules. Installing into a temp directory and renaming keeps a
// concurrent or interrupted install from leaving a half-filled cache.
func installedModules(t *testing.T) string {
	if os.Getenv(integrationVar) != "1" {
		t.Skipf("set %s=1 to run vitest, eslint and tsc for real (needs node and npm on PATH and network for npm ci)", integrationVar)
	}
	npmOnce.Do(func() { npmModules, npmErr = npmCI() })
	if npmErr != nil {
		t.Fatalf("npm ci of the pinned fixture failed: %v", npmErr)
	}
	return npmModules
}

func npmCI() (string, error) {
	pkg, err := os.ReadFile(filepath.Join(nodeFixtureDir, "package.json"))
	if err != nil {
		return "", err
	}
	lock, err := os.ReadFile(filepath.Join(nodeFixtureDir, "package-lock.json"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(pkg, lock...))
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("assure-ts-integration-%x", sum[:6]))
	modules := filepath.Join(dir, "node_modules")
	if exists(filepath.Join(modules, ".package-lock.json")) {
		return modules, nil
	}
	tmp, err := os.MkdirTemp(os.TempDir(), "assure-ts-npm-*")
	if err != nil {
		return "", err
	}
	for name, data := range map[string][]byte{"package.json": pkg, "package-lock.json": lock} {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o644); err != nil {
			return "", err
		}
	}
	cmd := exec.CommandContext(context.Background(), "npm", "ci", "--no-audit", "--no-fund")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	if err := os.Rename(tmp, dir); err != nil {
		if !errors.Is(err, fs.ErrExist) && !exists(modules) {
			return "", err
		}
		_ = os.RemoveAll(tmp)
	}
	return modules, nil
}

var realUnitFiles = map[string]string{
	".gitignore":        "node_modules\n",
	"tsconfig.json":     `{ "compilerOptions": { "strict": true, "module": "esnext", "moduleResolution": "bundler", "target": "es2022", "noEmit": true, "types": [] }, "include": ["src"] }`,
	"eslint.config.js":  "import tseslint from \"typescript-eslint\";\nexport default tseslint.config(tseslint.configs.recommended);\n",
	"src/parse.ts":      "export function parse(s: string): number {\n  return Number(s);\n}\n",
	"src/cli.ts":        "import { parse } from \"./parse\";\n\nexport function cli(s: string): number {\n  const n: number = parse(s);\n  return n + 1;\n}\n",
	"src/cli.test.ts":   "import { describe, it, expect } from \"vitest\";\nimport { cli } from \"./cli\";\n\ndescribe(\"cli\", () => {\n  it(\"adds one\", () => {\n    expect(cli(\"1\")).toBe(2);\n  });\n});\n",
	"src/other.test.ts": "import { it, expect } from \"vitest\";\n\nit(\"other\", () => {\n  expect(1).toBe(1);\n});\n",
	"src/lonely.ts":     "export const lonely = 1;\n",
}

// realUnit builds a committed git repository whose single unit uses the
// npm-installed tools through a node_modules symlink, and enters it.
func realUnit(t *testing.T) string {
	modules := installedModules(t)
	isolateGit(t)
	top := realTempDir(t)
	files := maps.Clone(realUnitFiles)
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := os.ReadFile(filepath.Join(nodeFixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	writeTree(t, top, files)
	if err := os.Symlink(modules, filepath.Join(top, "node_modules")); err != nil {
		t.Fatal(err)
	}
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	t.Chdir(top)
	return top
}

func nodeVersion(t *testing.T) string {
	out, err := exec.CommandContext(t.Context(), "node", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestIntegrationRelatedTestRuns(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{"src/parse.ts": "export function parse(s: string): number {\n  return Number(s.trim());\n}\n"})
	out := t.TempDir()
	resp := runIn(t, "VER-TESTS-PASS", out)
	if want := map[string]string{"node": nodeVersion(t), "vitest": "5.0.4"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v", resp.ToolVersions, want)
	}
	j := junitIn(t, out, resp)
	if got, want := outcomes(j), map[string]ev.Outcome{"src/cli.test.ts.cli > adds one": ev.Passed}; !maps.Equal(got, want) {
		t.Fatalf("cases %v, want %v; only src/parse.ts changed, and src/cli.test.ts reaches it through src/cli.ts, so its case must run while the unrelated src/other.test.ts must not", got, want)
	}
}

func TestIntegrationNoRelatedTestIsEmpty(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{"src/lonely.ts": "export const lonely = 2;\n"})
	out := t.TempDir()
	j := junitIn(t, out, runIn(t, "VER-TESTS-PASS", out))
	if j.Cases != 0 || j.Errors != 0 || j.Failures != 0 {
		t.Fatalf("JUnit %+v; no test imports src/lonely.ts, and the spec wants zero cases, not an error", j)
	}
}

func TestIntegrationImportErrorIsTestError(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{"src/broken.test.ts": "import { it } from \"vitest\";\nimport { x } from \"./nope\";\n\nit(\"never\", () => {\n  void x;\n});\n"})
	out := t.TempDir()
	j := junitIn(t, out, runIn(t, "VER-TESTS-PASS", out))
	var errored []ev.Failure
	for _, f := range j.Failing {
		if f.Error && strings.HasPrefix(f.Name, "src/broken.test.ts.") {
			errored = append(errored, f)
		}
	}
	if len(errored) != 1 || !strings.Contains(errored[0].Text, "./nope") {
		t.Fatalf("erroring cases for src/broken.test.ts: %+v (all failing: %+v); a test file that cannot load must be one erroring case carrying vitest's message, or VER-TESTS-PASS passes a file that never ran", errored, j.Failing)
	}
}

func TestIntegrationTypeErrorElsewhereReported(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{"src/parse.ts": "export function parse(s: string): string {\n  return s;\n}\n"})
	out := t.TempDir()
	resp := runIn(t, "CODE-ZERO-WARNINGS", out)
	if want := map[string]string{"node": nodeVersion(t), "eslint": "10.12.0", "typescript": "6.0.3"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v", resp.ToolVersions, want)
	}
	rs := sarifIn(t, out, resp)
	if !slices.ContainsFunc(rs, func(r ev.Result) bool {
		return r.URI == "src/cli.ts" && r.RuleID == "TS2322" && r.Level == "error"
	}) {
		t.Fatalf("results %+v; src/parse.ts changed its return type and the unchanged src/cli.ts no longer type-checks, so its TS2322 must be reported", rs)
	}
}
