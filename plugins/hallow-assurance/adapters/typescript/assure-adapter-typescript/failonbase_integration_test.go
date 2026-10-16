package main

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

const listTest = `import { describe, it, expect } from "vitest";
import { missing } from "./missing";

describe("outer", () => {
  describe("inner", () => {
    it("deep", () => {
      expect(missing).toBe(1);
    });
  });
  it.skip("skipped", () => {});
  it.each([1, 2])("each %i", (n) => {
    expect(n).toBeGreaterThan(0);
  });
});
it("top", () => {});
`

func TestIntegrationVitestListNamesAndLocations(t *testing.T) {
	top := realUnit(t)
	skipOnly := "import { it } from \"vitest\";\n\nit.skip(\"later\", () => {});\n"
	writeTree(t, top, map[string]string{"src/list.test.ts": listTest, "src/skip.test.ts": skipOnly})
	got, err := vitestList(top, filepath.Join(top, "node_modules", ".bin", "vitest"), top, absFiles(top, []string{"src/list.test.ts", "src/skip.test.ts"}))
	if err != nil {
		t.Fatalf("vitestList: %v", err)
	}
	lines := map[string]int{}
	for _, l := range got {
		if l.File != "src/list.test.ts" {
			t.Fatalf("listed %+v; a skip-only file lists nothing and files are repo-relative", l)
		}
		lines[l.Name] = l.Location.Line
	}
	// Run results are matched to listed tests by location, so each listed line must be the
	// test's own line; it.each is listed once under its template title.
	if want := map[string]int{"outer > inner > deep": 6, "outer > each %i": 11, "top": 15}; !maps.Equal(lines, want) {
		t.Fatalf("listing %v, want %v; names join describe titles and title with \" > \", a file whose import fails at run time is still listed, and skipped tests are not", lines, want)
	}
	for rel, want := range map[string]string{"src/list.test.ts": listTest, "src/skip.test.ts": skipOnly} {
		if text, _ := fileText(t, filepath.Join(top, rel)); text != want {
			t.Fatalf("%s now reads %q; vitest list --json <file> takes the file as its output path and overwrites the test", rel, text)
		}
	}
}

func TestIntegrationFailOnBase(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/range.ts":      "export function inRange(n: number, lo: number, hi: number): boolean {\n  return n >= lo && n < hi;\n}\n",
		"src/range.test.ts": "import { describe, it, expect } from \"vitest\";\nimport { inRange } from \"./range\";\n\ndescribe(\"range\", () => {\n  it(\"parses\", () => {\n    expect(inRange(1, 0, 5)).toBe(true);\n  });\n});\n",
	})
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "range")
	writeTree(t, top, map[string]string{
		"src/range.ts": "export function inRange(n: number, lo: number, hi: number): boolean {\n  return n >= lo && n <= hi;\n}\n",
		"src/range.test.ts": `import { describe, it, expect } from "vitest";
import { inRange } from "./range";

describe("range", () => {
  it("parses", () => {
    expect(inRange(5, 0, 5)).toBe(true);
  });
  it("includes the end", () => {
    expect(inRange(5, 0, 5)).toBe(true);
  });
  it("excludes below", () => {
    expect(inRange(-1, 0, 5)).toBe(false);
  });
  it.each([4, 5])("edge %i", (n) => {
    expect(inRange(n, 0, 5)).toBe(true);
  });
});
`,
		"src/span.ts":      "export const span = (lo: number, hi: number): number => hi - lo + 1;\n",
		"src/span.test.ts": "import { it, expect } from \"vitest\";\nimport { span } from \"./span\";\n\nit(\"counts both ends\", () => {\n  expect(span(0, 5)).toBe(6);\n});\n",
	})
	before := stateOf(t, top)
	out := t.TempDir()
	resp := runIn(t, "VER-FAIL-ON-BASE", out)
	if after := stateOf(t, top); after != before {
		t.Fatalf("repository state changed from %+v to %+v; the base tree is built outside the repository", before, after)
	}
	if want := map[string]string{"node": nodeVersion(t), "vitest": "5.0.4"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v", resp.ToolVersions, want)
	}
	j := failOnBaseIn(t, out, resp)
	want := map[string]ev.Outcome{
		"src/range.test.ts.range > includes the end": ev.Failed,
		"src/range.test.ts.range > excludes below":   ev.Passed,
		"src/range.test.ts.range > edge %i":          ev.Failed,
		"src/span.test.ts.counts both ends":          ev.Errored,
	}
	if got := outcomes(j); !maps.Equal(got, want) {
		t.Fatalf("cases %v, want %v; the edited range > parses existed at base and is not selected, the regression test fails on the old off-by-one, one failing it.each row fails its case, and a test needing a module absent at base errors", got, want)
	}
	if text := failingText(j, "src/span.test.ts.counts both ends"); !strings.Contains(text, "./span") {
		t.Fatalf("error text %q; it must carry vitest's message naming the missing module", text)
	}
}
