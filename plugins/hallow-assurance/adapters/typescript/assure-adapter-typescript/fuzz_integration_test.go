package main

import (
	"maps"
	"slices"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func TestIntegrationFuzz(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/parse.ts": "export function parse(s: string): number {\n  return Number(s.trim());\n}\n",
		"src/cli.ts":   "import { parse } from \"./parse\";\n\nexport function cli(s: string): number {\n  return parse(s) + 1;\n}\n",
		"src/parse.fuzz.test.ts": `import { it } from "vitest";
import fc from "fast-check";
import { parse } from "./parse";

it("round trips integers", () => {
  fc.assert(fc.property(fc.integer(), (n) => parse(String(n)) === n));
});

it("is never NaN", () => {
  fc.assert(fc.property(fc.string(), (s) => !Number.isNaN(parse(s))));
});
`,
	})
	out := t.TempDir()
	resp := runIn(t, "VER-ROBUST-FUZZ", out)
	if want := map[string]string{"node": nodeVersion(t), "vitest": "5.0.4", "fast-check": "4.10.2"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v", resp.ToolVersions, want)
	}
	got := fuzzSuites(t, fuzzIn(t, out, resp))
	parse := []ev.Case{
		{Name: "src/parse.fuzz.test.ts.round trips integers", Outcome: ev.Passed},
		{Name: "src/parse.fuzz.test.ts.is never NaN", Outcome: ev.Failed},
	}
	if len(got) != 2 || len(got["src/cli.ts"]) != 0 || !slices.Equal(got["src/parse.ts"], parse) {
		t.Fatalf("suites %+v; want src/parse.ts with its two properties (the NaN one failing) and src/cli.ts, which has no fuzz file, with zero cases", got)
	}
}
