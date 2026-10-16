package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func packResults(t *testing.T, obj string) []ev.Result {
	out := t.TempDir()
	resp := runIn(t, obj, out)
	if resp.Environment != nil {
		t.Fatalf("%s: environment %q", obj, resp.Environment.Message)
	}
	if want := map[string]string{"node": nodeVersion(t), "eslint": "10.12.0", "typescript-eslint": "8.71.2", "typescript": "6.0.3"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("%s: tool_versions %v, want %v; the spec requires eslint, typescript-eslint and typescript", obj, resp.ToolVersions, want)
	}
	return sarifIn(t, out, resp)
}

func ruleCounts(rs []ev.Result) map[string]int {
	got := map[string]int{}
	for _, r := range rs {
		got[fmt.Sprintf("%s %s %s", r.URI, r.RuleID, r.Level)]++
	}
	return got
}

func TestIntegrationRepoConfigCannotDisablePack(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{"eslint.config.js": "import tseslint from \"typescript-eslint\";\nexport default tseslint.config(tseslint.configs.recommended, { rules: { \"@typescript-eslint/no-floating-promises\": \"off\" } });\n"})
	git(t, top, "commit", "-qam", "repo turns the rule off")
	writeTree(t, top, map[string]string{"src/drop.ts": `export function dropped(): void {
  Promise.resolve(1);
}

export function inline(): void {
  // eslint-disable-next-line @typescript-eslint/no-floating-promises
  Promise.resolve(2);
}
`})
	got := ruleCounts(packResults(t, "CODE-CHECK-RETURNS"))
	if want := map[string]int{"src/drop.ts @typescript-eslint/no-floating-promises error": 2}; !maps.Equal(got, want) {
		t.Fatalf("results %v, want %v; neither the repository's ESLint configuration nor an inline disable may suppress a pack finding, and the noInlineConfig notice is not a finding", got, want)
	}
}

func TestIntegrationComplexityIsRaw(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/cx.ts": `export function cx(a: boolean, b: boolean): number {
  if (a) return 1;
  if (b) return 2;
  return 3;
}

export const flat = (): number => 1;
`,
		"src/cx.test.ts": "export function branchy(a: boolean, b: boolean): number {\n  return a ? 1 : b ? 2 : 3;\n}\n",
	})
	got := packResults(t, "CODE-COMPLEXITY")
	want := []ev.Result{
		{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of Function 'cx'", URI: "src/cx.ts", Metric: 3, HasMetric: true},
		{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of Arrow function", URI: "src/cx.ts", Metric: 1, HasMetric: true},
	}
	sameResults(t, "one note per function with ESLint's cyclomatic complexity, including complexity 1 (no threshold), and nothing for the test file", got, want)
}

func TestIntegrationUnsafeIgnoresTestFiles(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{"src/a.test.ts": "export const n = 1 as unknown as string;\n"})
	if rs := packResults(t, "CODE-NO-UNSAFE"); len(rs) != 0 {
		t.Fatalf("results %+v; only a test file has the double cast, and test files are not unsafe findings", rs)
	}
}

func TestIntegrationOwnRulesPositiveAndNegative(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/double-cast.ts": "export const d = 1 as unknown as string;\n",
		"src/reflect.ts":     "export const r = Reflect.ownKeys({});\n",
		"src/proxy.ts":       "export const p = new Proxy({}, {});\n",
		"src/fetch-signal.ts": `declare const u: string;
declare const window: { fetch: (u: string, init?: object) => Promise<unknown> };
declare const self: { fetch: (u: string, init?: object) => Promise<unknown> };

export async function f(): Promise<void> {
  await fetch(u);
  await fetch(u, { method: "POST" });
  await fetch(u, { headers: { signal: "x" } });
  await globalThis.fetch(u);
  await window.fetch(u, { method: "POST" });
  await self.fetch(u);
}
`,
		"src/loop.ts": "export function spin(): void {\n  while (true) {\n    break;\n  }\n}\n",
		"src/negatives.ts": `declare const u: string;
declare const s: object;
declare const init: object;
declare const self: { fetch: (u: string, init?: object) => Promise<unknown> };
const o = { Reflect: 1, Proxy: Object, fetch: (u: string) => u };

export const single = 1 as unknown;
export const plain = "x" as string;
export const r = o.Reflect;
export const p = new o.Proxy();

export async function f(signal: object): Promise<void> {
  await fetch(u, { signal: s });
  await fetch(u, init);
  await fetch(u, { "signal": s });
  await fetch(u, { signal });
  await self.fetch(u, { signal: s });
  o.fetch(u);
  await globalThis["fetch"](u);
}

export function bounded(n: number): void {
  let i = 0;
  while (i < n) {
    i++;
  }
}
`,
	})
	cases := map[string]map[string]int{
		"CODE-NO-UNSAFE": {
			"src/double-cast.ts double-cast error": 1,
			"src/reflect.ts reflect error":         1,
			"src/proxy.ts proxy error":             1,
		},
		"CODE-RESOURCE-BOUNDS": {
			"src/fetch-signal.ts fetch-signal error":  6,
			"src/loop.ts no-constant-condition error": 1,
		},
	}
	for _, obj := range slices.Sorted(maps.Keys(cases)) {
		rs := packResults(t, obj)
		if got := ruleCounts(rs); !maps.Equal(got, cases[obj]) {
			t.Fatalf("%s: results %v, want %v; each positive fixture is reported once per offending expression (fetch, globalThis.fetch, window.fetch or self.fetch with no init, an init literal without signal, a signal nested elsewhere; while (true) under checkLoops all), and src/negatives.ts (a single cast, a member named Reflect or Proxy, fetch or self.fetch with a signal, a non-literal init, a quoted or shorthand signal key, fetch on a non-global object, the computed globalThis[\"fetch\"] which the pack accepts, a bounded loop) has none", obj, got, cases[obj])
		}
		for _, r := range rs {
			if r.RuleID == "fetch-signal" && !strings.Contains(r.Message, "AbortSignal") {
				t.Fatalf("fetch-signal message %q does not say what is missing", r.Message)
			}
		}
	}
}

func TestIntegrationUnparsableFailsClosed(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/syntax.ts":    "export const = ;\n",
		"tools/outside.ts": "export const outside = 1;\n",
	})
	for _, obj := range slices.Sorted(maps.Keys(packObjectives)) {
		got := ruleCounts(packResults(t, obj))
		for _, key := range []string{"src/syntax.ts parse error", "tools/outside.ts parse error"} {
			if got[key] != 1 {
				t.Fatalf("%s: results %v lack %q; a file with a syntax error, or one outside the unit's tsconfig.json that the typed parser cannot load, was not checked, so the pack must fail on it", obj, got, key)
			}
		}
	}
}
