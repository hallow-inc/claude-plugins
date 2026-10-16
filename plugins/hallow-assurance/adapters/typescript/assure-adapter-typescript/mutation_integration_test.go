package main

import (
	"maps"
	"os/exec"
	"slices"
	"testing"
)

func gitStatus(t *testing.T, dir string) string {
	cmd := exec.Command("git", "status", "--porcelain=v1", "--ignored", "--untracked-files=all")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestIntegrationMutationChangedLinesOnly(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/sign.ts": `export function positive(n: number): boolean {
  if (n > 1) {
    return true;
  }
  return false;
}

export function double(n: number): number {
  return n > 100 ? n * 2 : n;
}
`,
		"src/sign.test.ts": "import { it, expect } from \"vitest\";\nimport { positive } from \"./sign\";\n\nit(\"signs\", () => {\n  expect(positive(5)).toBe(true);\n  expect(positive(-5)).toBe(false);\n});\n",
	})
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "sign")
	changed := `export function positive(n: number): boolean {
  if (n > 0) {
    return true;
  }
  return false;
}

export function double(n: number): number {
  return n > 100 ? n * 2 : n;
}
`
	writeTree(t, top, map[string]string{
		"src/sign.ts":  changed,
		"src/fresh.ts": "export function fresh(n: number): number {\n  return n > 3 ? 1 : 0;\n}\n",
	})
	before := gitStatus(t, top)

	out := t.TempDir()
	resp := runIn(t, "VER-MUTATION-CHANGED", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q", resp.Environment.Message)
	}
	if want := map[string]string{"node": nodeVersion(t), "vitest": "5.0.4", "@stryker-mutator/core": "10.0.0", "@stryker-mutator/vitest-runner": "10.0.0"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v", resp.ToolVersions, want)
	}
	rep, _ := mutationIn(t, out, resp)
	if got := slices.Sorted(maps.Keys(rep.Files)); !slices.Equal(got, []string{"src/sign.ts"}) {
		t.Fatalf("files %v, want only src/sign.ts; the untracked src/fresh.ts and the unchanged test file are not mutated", got)
	}
	f := rep.Files["src/sign.ts"]
	if f.Language != "typescript" || f.Source != changed {
		t.Fatalf("language %q, source %q; the report carries the file's current source", f.Language, f.Source)
	}
	survived := false
	for _, m := range f.Mutants {
		if m.Location.Start.Line != 2 || m.Location.End.Line != 2 {
			t.Fatalf("mutant %+v outside line 2; only the changed line is mutated, so the untested double() on line 9 must not appear", m)
		}
		if m.MutatorName == "EqualityOperator" && m.Replacement == "n >= 0" && m.Status == "Survived" {
			survived = true
		}
	}
	if !survived {
		t.Fatalf("mutants %+v lack a Survived n >= 0; no test distinguishes n > 0 from n >= 0", f.Mutants)
	}
	if after := gitStatus(t, top); after != before {
		t.Fatalf("git status changed from\n%s\nto\n%s\nthe run must leave the repository untouched", before, after)
	}
}
