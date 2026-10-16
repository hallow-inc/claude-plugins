package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

const decideSource = `export const LIMIT = 10;

export const half = (n: number): number => n / 2;

// padding
// padding
// padding
// padding
// padding
export function decide(n: number): string {
  if (n > LIMIT) {
    return "big";
  }
  if (n < 0) {
    return "negative";
  }
  const h = half(n);
  void h;
  return "small";
}
`

func TestIntegrationCoverage(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/decide.ts": decideSource,
		"src/decide.test.ts": `import { it, expect } from "vitest";
import fc from "fast-check";
import { decide } from "./decide";

it("classifies every integer", () => {
  fc.assert(fc.property(fc.integer(), (n) => ["big", "negative", "small"].includes(decide(n))));
});

it("fails", () => {
  expect(decide(1)).toBe("big");
});
`,
	})
	var runs [2][]byte
	for i := range runs {
		out := t.TempDir()
		resp := runIn(t, "VER-COVERAGE-RESOLUTION", out)
		if resp.ToolVersions["@vitest/coverage-v8"] != "5.0.4" {
			t.Fatalf("tool_versions %v lack @vitest/coverage-v8 5.0.4", resp.ToolVersions)
		}
		data, err := os.ReadFile(filepath.Join(out, "coverage.lcov"))
		if err != nil {
			t.Fatalf("run %d wrote no coverage.lcov although a test failed: %v", i, err)
		}
		runs[i] = data
	}
	if string(runs[0]) != string(runs[1]) {
		t.Fatalf("two coverage runs on one commit differ:\n%s\n----\n%s\nfast-check draws different inputs each run, so hit counts must not reach the evidence", runs[0], runs[1])
	}
	files, err := ev.ParseLCOV(runs[0])
	if err != nil || len(files) != 1 || files[0].Path != "src/decide.ts" {
		t.Fatalf("LCOV %v (%v):\n%s\nwant one record for the changed src/decide.ts only", files, err, runs[0])
	}
	if !slices.ContainsFunc(files[0].Functions, func(f ev.LCOVFunction) bool {
		return f.Name == "decide" && f.Start == 10 && f.End == 20
	}) {
		t.Fatalf("functions %+v; decide is declared on line 10 and closes on line 20", files[0].Functions)
	}
	if files[0].Lines[12] != 1 || files[0].Lines[15] != 1 {
		t.Fatalf("DA %v; the property drives decide through both early returns", files[0].Lines)
	}
}
