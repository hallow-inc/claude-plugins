package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func scopeJSON(t fataler, s adapterkit.Scope) []byte {
	entries := make([]map[string]any, len(s))
	for i, e := range s {
		entries[i] = map[string]any{"path": e.Path}
		if e.Ranges != nil {
			entries[i]["ranges"] = e.Ranges
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return data
}

func writeScopeFile(t fataler, dir string, data []byte) string {
	p := filepath.Join(dir, "scope.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	return p
}

func runArgs(args ...string) (int, []byte, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.Bytes(), e.String()
}

func runScoped(t fataler, obj, scope, out string) runResponse {
	code, raw, stderr := runArgs("run", obj, "--scope", scope, "--out", out)
	if code != 0 {
		t.Fatalf("run %s --scope exited %d: %s", obj, code, stderr)
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, raw); err != nil || len(vs) > 0 {
		t.Fatalf("run %s --scope: response %s fails the adapter-run schema: %v %v", obj, raw, err, vs)
	}
	var resp runResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v", err)
	}
	return resp
}

func ranTools(t fataler, r strykerRepo) []string {
	var ran []string
	if callOf(t, r.unitData, "vitest").invoked {
		ran = append(ran, "vitest")
	}
	if strykerCallOf(t, r.unitData).invoked {
		ran = append(ran, "stryker")
	}
	return ran
}

func objectiveNames() []string {
	return slices.Sorted(maps.Keys(runners))
}

func TestRunRejectsBothSelectorsOrNeither(t *testing.T) {
	r := newStrykerRepo(t)
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "packages/a/src/x.ts"}}))
	for _, obj := range objectiveNames() {
		out := filepath.Join(t.TempDir(), "ev")
		for _, args := range [][]string{
			{"--changed-from", "HEAD", "--scope", scope, "--out", out},
			{"--out", out},
			{"--scope", scope},
			{"--changed-from", "HEAD"},
		} {
			code, stdout, stderr := runArgs(append([]string{"run", obj}, args...)...)
			if code != 2 || len(stdout) != 0 || !strings.Contains(stderr, "usage:") || !strings.Contains(stderr, "--scope") {
				t.Fatalf("%s %q: exit %d stdout %q stderr %q; both selectors name two change sets and neither names none, so either is a usage error (2) printing the usage line with both selectors, never a response", obj, args, code, stdout, stderr)
			}
			if ran := ranTools(t, r); len(ran) != 0 {
				t.Fatalf("%s %q ran %v before rejecting; a usage error must cost nothing", obj, args, ran)
			}
			if adapterkit.Exists(out) {
				t.Fatalf("%s %q created %s; a rejected run leaves no evidence directory", obj, args, out)
			}
		}
	}
}

func TestFailOnBaseWithScopeIsUsageError(t *testing.T) {
	r := newStrykerRepo(t)
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "packages/a/src/x.ts", Ranges: [][2]int{{1, 2}}}}))
	out := filepath.Join(t.TempDir(), "ev")
	code, stdout, stderr := runArgs("run", "VER-FAIL-ON-BASE", "--scope", scope, "--out", out)
	if code != 2 || len(stdout) != 0 {
		t.Fatalf("VER-FAIL-ON-BASE --scope exit %d stdout %q stderr %q, want 2 and no response; fail-on-base reruns tests on a base commit and a scope has none, so any response reports a comparison that never happened", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--changed-from") {
		t.Fatalf("stderr %q does not name --changed-from; the caller must learn which selector fail-on-base needs", stderr)
	}
	if ran := ranTools(t, r); len(ran) != 0 || adapterkit.Exists(out) {
		t.Fatalf("VER-FAIL-ON-BASE --scope ran %v or created %s before refusing", ran, out)
	}
}

var scopeCorruptions = []string{"reversed range", "zero bound", "empty scope", "unknown key", "truncated", "missing file"}

func TestMalformedScopeIsUsageError(t *testing.T) {
	r := newStrykerRepo(t)
	paths := []string{"packages/a/src/x.ts", "packages/a/src/x.test.ts", "packages/a/package.json"}
	drawScope := func(t *rapid.T) adapterkit.Scope {
		var s adapterkit.Scope
		for range rapid.IntRange(1, 3).Draw(t, "entries") {
			e := adapterkit.ScopeEntry{Path: rapid.SampledFrom(paths).Draw(t, "path")}
			for range rapid.IntRange(0, 2).Draw(t, "ranges") {
				lo := rapid.IntRange(1, 9).Draw(t, "lo")
				e.Ranges = append(e.Ranges, [2]int{lo, lo + rapid.IntRange(0, 4).Draw(t, "span")})
			}
			s = append(s, e)
		}
		return s
	}
	valid := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "packages/a/src/x.test.ts", Ranges: [][2]int{{1, 1}}}}))
	replay(t, r.unitData, "vitest", replayed{report: `{"testResults":[]}`})
	if code, _, stderr := runArgs("run", "VER-TESTS-PASS", "--scope", valid, "--out", t.TempDir()); code != 0 {
		t.Fatalf("well-formed scope exited %d: %s; the property below needs a scope the adapter accepts to mean anything", code, stderr)
	}
	replay(t, r.unitData, "vitest", replayed{noReport: true})
	rapid.Check(t, func(rt *rapid.T) {
		s := drawScope(rt)
		how := rapid.SampledFrom(scopeCorruptions).Draw(rt, "corruption")
		i := rapid.IntRange(0, len(s)-1).Draw(rt, "entry")
		var data []byte
		switch how {
		case "reversed range":
			lo := rapid.IntRange(2, 9).Draw(rt, "hi")
			s[i].Ranges = append(s[i].Ranges, [2]int{lo, rapid.IntRange(1, lo-1).Draw(rt, "lo")})
		case "zero bound":
			s[i].Ranges = append(s[i].Ranges, [2]int{0, rapid.IntRange(0, 3).Draw(rt, "end")})
		case "empty scope":
			s = adapterkit.Scope{}
		}
		data = scopeJSON(rt, s)
		switch how {
		case "unknown key":
			data = bytes.Replace(data, []byte(`{"path"`), []byte(`{"lines":[1],"path"`), 1)
		case "truncated":
			data = data[:rapid.IntRange(0, len(data)-1).Draw(rt, "cut")]
		}
		dir := t.TempDir()
		file := writeScopeFile(rt, dir, data)
		if how == "missing file" {
			file = filepath.Join(dir, "absent.json")
		}
		obj := rapid.SampledFrom(objectiveNames()).Draw(rt, "objective")
		out := filepath.Join(dir, "ev")
		code, stdout, stderr := runArgs("run", obj, "--scope", file, "--out", out)
		if code != 2 || len(stdout) != 0 || stderr == "" {
			rt.Fatalf("%s with %s scope %s: exit %d stdout %q stderr %q; a scope the schema or range check rejects is a usage error (2) on stderr, never a run over a guessed selection", obj, how, data, code, stdout, stderr)
		}
		if ran := ranTools(rt, r); len(ran) != 0 || adapterkit.Exists(out) {
			rt.Fatalf("%s with %s scope ran %v or created %s before rejecting", obj, how, ran, out)
		}
	})
}

func TestScopedUnchangedFileSelectsAsIfChanged(t *testing.T) {
	candidates := []string{
		"src/parse.ts", "src/lint.ts", "src/cli.test.ts", "src/env.d.ts", "README.md", "tsconfig.json",
		"packages/b/package.json", "packages/b/src/y.ts", "packages/b/src/y.test.ts", "packages/b/src/gen.ts",
	}
	r := newRepo(t, map[string]string{
		"packages/b/package.json":  "{}",
		"packages/b/src/y.ts":      "export const y = 1;\n",
		"packages/b/src/y.test.ts": "",
		"packages/b/src/gen.ts":    "// Code generated by gen. DO NOT EDIT.\nexport const g = 1;\n",
	}, nil)
	rapid.Check(t, func(rt *rapid.T) {
		picked := rapid.SliceOfNDistinct(rapid.SampledFrom(candidates), 1, len(candidates), rapid.ID[string]).Draw(rt, "scoped")
		var s adapterkit.Scope
		for _, p := range picked {
			e := adapterkit.ScopeEntry{Path: p}
			if rapid.Bool().Draw(rt, "ranged") {
				e.Ranges = [][2]int{{1, 1}}
			}
			s = append(s, e)
		}
		paths, err := selectedPaths(r.top, adapterkit.RunArgs{Scope: s})
		if err != nil {
			rt.Fatalf("%v", err)
		}
		scoped, msg, err := selectUnits(r.top, r.top, paths)
		if err != nil || msg != "" {
			rt.Fatalf("scoped selection: %v %q", err, msg)
		}
		orig := map[string][]byte{}
		for _, p := range picked {
			full := filepath.Join(r.top, filepath.FromSlash(p))
			b, err := os.ReadFile(full)
			if err != nil {
				rt.Fatalf("%v", err)
			}
			orig[full] = b
			if err := os.WriteFile(full, append(slices.Clone(b), "\n// edited\n"...), 0o644); err != nil {
				rt.Fatalf("%v", err)
			}
		}
		changed, err := adapterkit.ChangedPaths(r.top, "HEAD")
		for full, b := range orig {
			if werr := os.WriteFile(full, b, 0o644); werr != nil {
				rt.Fatalf("%v", werr)
			}
		}
		if err != nil {
			rt.Fatalf("%v", err)
		}
		want, _, err := selectUnits(r.top, r.top, changed)
		if err != nil {
			rt.Fatalf("%v", err)
		}
		if !reflect.DeepEqual(scoped, want) {
			rt.Fatalf("scope %v on a clean tree selects %v; editing the same files selects %v; a file listed in --scope must be tested exactly as if it had changed, or an audit of committed code skips the units it names", picked, scoped, want)
		}
	})
}

func TestScopedTestsIgnoreTheDiff(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
	writeTree(t, r.top, map[string]string{"src/lint.ts": "export const lint = 2;\n"})
	replay(t, r.data, "vitest", replayed{report: `{"testResults":[]}`})
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "src/parse.ts", Ranges: [][2]int{{1, 1}}}}))
	out := t.TempDir()
	junitIn(t, out, runScoped(t, "VER-TESTS-PASS", scope, out))
	c := callOf(t, r.data, "vitest")
	if want := []string{filepath.Join(r.top, "src", "parse.ts")}; !c.invoked || !slices.Equal(c.files(), want) {
		t.Fatalf("vitest call %+v, want files %q; the scoped src/parse.ts runs though it is unchanged, and the changed src/lint.ts outside the scope does not", c, want)
	}
}

func TestScopedMutateEntriesAreWholeSourceFiles(t *testing.T) {
	top := realTempDir(t)
	marker := filepath.Join(t.TempDir(), "git-ran")
	bin := t.TempDir()
	exe(t, filepath.Join(bin, "git"), "#!/bin/sh\necho \"$@\" >> '"+marker+"'\nexit 1\n")
	t.Setenv("PATH", bin)
	t.Chdir(top)
	iter := 0
	rapid.Check(t, func(rt *rapid.T) {
		iter++
		unitRel := rapid.SampledFrom([]string{".", "packages/a"}).Draw(rt, "unit")
		unit := filepath.Join(top, filepath.FromSlash(unitRel))
		var sel, want []string
		files := map[string]string{}
		for j := range rapid.IntRange(0, 6).Draw(rt, "files") {
			kind := rapid.SampledFrom([]string{"source", "generated-header", "declaration", "test", "config"}).Draw(rt, "kind")
			name, body := fmt.Sprintf("src/f%d_%d.ts", iter, j), "export const x = 1;\n"
			switch kind {
			case "generated-header":
				body = "// Code generated by gen. DO NOT EDIT.\n" + body
			case "declaration":
				name = fmt.Sprintf("src/f%d_%d.d.ts", iter, j)
			case "test":
				name = fmt.Sprintf("src/f%d_%d.test.ts", iter, j)
			case "config":
				name = fmt.Sprintf("d%d_%d/vitest.config.ts", iter, j)
			}
			p := name
			if unitRel != "." {
				p = unitRel + "/" + name
			}
			files[p] = body
			sel = append(sel, p)
			if kind == "source" {
				want = append(want, name)
			}
		}
		writeTree(rt, top, files)
		got, err := mutateEntries(runContext{root: top, ref: "no-such-ref", scope: adapterkit.Scope{{Path: "unused"}}, sel: map[string][]string{unit: sel}}, unit)
		if err != nil {
			rt.Fatalf("mutateEntries in scope mode: %v; a scoped run has no ref, so it must not ask git for a diff", err)
		}
		if !slices.Equal(got, want) {
			rt.Fatalf("selected %q in unit %s gave mutate entries %q, want %q; a scoped run mutates each source file whole, named relative to the unit with no line range, and the scope filters afterwards; a range here would skip the unchanged lines the audit asked about", sel, unitRel, got, want)
		}
		if ran, err := os.ReadFile(marker); err == nil {
			rt.Fatalf("git ran in scope mode: %s; a scope names lines, not a diff", ran)
		}
	})
}

func scopeHas(s adapterkit.Scope, path string, line int) bool {
	for _, e := range s {
		if e.Path != path {
			continue
		}
		if e.Ranges == nil {
			return true
		}
		for _, r := range e.Ranges {
			if r[0] <= line && line <= r[1] {
				return true
			}
		}
	}
	return false
}

const scopedMaxLine = 12

var scopedPaths = []string{"packages/a/src/x.ts", "packages/a/src/y.ts", "lib/z.ts"}

func drawScopedReport(t *rapid.T) (rep strykerReport, all []strykerMutant, owner map[string]string) {
	rep, owner = newStrykerReport(), map[string]string{}
	for _, p := range scopedPaths {
		if !rapid.Bool().Draw(t, "present") {
			continue
		}
		f := &strykerFile{Language: "typescript", Source: sourceOf(p), Mutants: []strykerMutant{}}
		for i := range rapid.IntRange(0, 6).Draw(t, "mutants") {
			m := strykerMutant{ID: fmt.Sprintf("%s#%d", p, i), MutatorName: "BooleanLiteral", Status: rapid.SampledFrom([]string{"Killed", "Survived", "NoCoverage"}).Draw(t, "status")}
			m.Location.Start = strykerPosition{Line: rapid.IntRange(1, scopedMaxLine).Draw(t, "start"), Column: 1}
			m.Location.End = strykerPosition{Line: m.Location.Start.Line + rapid.IntRange(0, 4).Draw(t, "span"), Column: 2}
			f.Mutants = append(f.Mutants, m)
			all = append(all, m)
			owner[m.ID] = p
		}
		rep.Files[p] = f
	}
	return rep, all, owner
}

func drawRangedScope(t *rapid.T) adapterkit.Scope {
	var s adapterkit.Scope
	for range rapid.IntRange(1, 3).Draw(t, "entries") {
		e := adapterkit.ScopeEntry{Path: rapid.SampledFrom(append(slices.Clone(scopedPaths), "packages/a/src/none.ts")).Draw(t, "path")}
		if !rapid.Bool().Draw(t, "wholeFile") {
			for range rapid.IntRange(1, 2).Draw(t, "ranges") {
				lo := rapid.IntRange(1, scopedMaxLine).Draw(t, "lo")
				e.Ranges = append(e.Ranges, [2]int{lo, rapid.IntRange(lo, scopedMaxLine).Draw(t, "hi")})
			}
		}
		s = append(s, e)
	}
	return s
}

func TestKeepScopedKeepsExactlyMutantsStartingInScope(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep, all, owner := drawScopedReport(t)
		s := drawRangedScope(t)
		want := map[string][]string{}
		for _, m := range all {
			if p := owner[m.ID]; scopeHas(s, p, m.Location.Start.Line) {
				want[p] = append(want[p], m.ID)
			}
		}
		rep.keepScoped(s)
		got := map[string][]string{}
		for p, f := range rep.Files {
			if len(f.Mutants) == 0 {
				t.Fatalf("file %s kept with no mutants; a file outside every range would appear in the evidence as audited", p)
			}
			if f.Source != sourceOf(p) || f.Language != "typescript" {
				t.Fatalf("file %s source %q language %q changed by filtering", p, f.Source, f.Language)
			}
			for _, m := range f.Mutants {
				got[p] = append(got[p], m.ID)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scope %+v kept %v, want %v; a mutant is kept iff its start line is in the scope (D5), so one starting outside is evidence the audit did not ask about, and a dropped one starting inside hides a survivor", s, got, want)
		}
		data, err := rep.encode()
		if err != nil {
			t.Fatalf("filtered report fails the mutation-report schema: %v", err)
		}
		if _, err := ev.ParseMutationReport(data); err != nil {
			t.Fatalf("assure rejects the filtered report: %v\n%s", err, data)
		}
	})
}

func TestRunScopedMutationKeepsOnlyInScopeMutants(t *testing.T) {
	r := newStrykerRepo(t)
	replay(t, r.unitData, "stryker", replayed{report: `{"schemaVersion":"1","files":{
		"src/x.ts":{"language":"typescript","source":"S","mutants":[
			{"id":"0","mutatorName":"BooleanLiteral","status":"Survived","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":9}}},
			{"id":"1","mutatorName":"BlockStatement","status":"Survived","location":{"start":{"line":1,"column":40},"end":{"line":3,"column":2}}},
			{"id":"2","mutatorName":"EqualityOperator","replacement":"n >= 1","status":"Survived","location":{"start":{"line":2,"column":10},"end":{"line":2,"column":15}}},
			{"id":"3","mutatorName":"ConditionalExpression","replacement":"true","status":"Killed","location":{"start":{"line":2,"column":10},"end":{"line":3,"column":1}}},
			{"id":"4","mutatorName":"BooleanLiteral","status":"Survived","location":{"start":{"line":3,"column":1},"end":{"line":3,"column":2}}}]},
		"src/y.ts":{"language":"typescript","source":"T","mutants":[
			{"id":"5","mutatorName":"BooleanLiteral","status":"Survived","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":2}}}]}}}`})
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{
		{Path: "packages/a/src/x.ts", Ranges: [][2]int{{2, 2}}},
		{Path: "packages/a/src/gen.ts"},
		{Path: "packages/a/src/x.test.ts"},
	}))
	out := t.TempDir()
	resp := runScoped(t, "VER-MUTATION-CHANGED", scope, out)
	if resp.Environment != nil {
		t.Fatalf("environment %q", resp.Environment.Message)
	}
	c := strykerCallOf(t, r.unitData)
	if !c.invoked || !slices.Equal(c.config.Mutate, []string{"src/x.ts"}) {
		t.Fatalf("stryker invoked %v with mutate %q, want [src/x.ts]; the scoped src/x.ts is unchanged, so a diff-hunk range would mutate nothing, and the generated and test files are never mutated", c.invoked, c.config.Mutate)
	}
	rep, ms := mutationIn(t, out, resp)
	if got := slices.Sorted(maps.Keys(rep.Files)); !slices.Equal(got, []string{"packages/a/src/x.ts"}) {
		t.Fatalf("files %v, want only packages/a/src/x.ts; src/y.ts is outside the scope", got)
	}
	var starts []string
	for _, m := range rep.Files["packages/a/src/x.ts"].Mutants {
		starts = append(starts, fmt.Sprintf("%s@%d-%d", m.MutatorName, m.Location.Start.Line, m.Location.End.Line))
	}
	if want := []string{"EqualityOperator@2-2", "ConditionalExpression@2-3"}; !slices.Equal(starts, want) || len(ms) != 2 {
		t.Fatalf("kept mutants %v, want %v; range [2,2] keeps the mutants starting on line 2, including the one ending on line 3, and drops those starting on lines 1 and 3, including the one spanning line 2", starts, want)
	}
}

func TestIntegrationScopedMutation(t *testing.T) {
	top := realUnit(t)
	writeTree(t, top, map[string]string{
		"src/sign.ts": `export function positive(n: number): boolean {
  if (n > 0) {
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
	before := gitStatus(t, top)
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "src/sign.ts", Ranges: [][2]int{{9, 9}}}}))

	out := t.TempDir()
	resp := runScoped(t, "VER-MUTATION-CHANGED", scope, out)
	if resp.Environment != nil {
		t.Fatalf("environment %q", resp.Environment.Message)
	}
	rep, _ := mutationIn(t, out, resp)
	if got := slices.Sorted(maps.Keys(rep.Files)); !slices.Equal(got, []string{"src/sign.ts"}) {
		t.Fatalf("files %v, want only src/sign.ts", got)
	}
	ms := rep.Files["src/sign.ts"].Mutants
	if len(ms) == 0 {
		t.Fatal("no mutants on line 9; the scoped, unchanged double() must be mutated though git sees no change")
	}
	for _, m := range ms {
		if m.Location.Start.Line != 9 {
			t.Fatalf("mutant %+v starts outside line 9; Stryker mutates the whole file and the scope keeps only line 9", m)
		}
	}
	if after := gitStatus(t, top); after != before {
		t.Fatalf("git status changed from\n%s\nto\n%s\nthe run must leave the repository untouched", before, after)
	}
}
