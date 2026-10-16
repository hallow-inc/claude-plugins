package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

type mutantKey struct {
	file                 string
	sl, sc, el, ec       int
	mutator, replacement string
}

type genMutant struct {
	key        mutantKey
	id, status string
}

var (
	mutatorGen     = rapid.SampledFrom([]string{"ConditionalExpression", "EqualityOperator", "BlockStatement", "StringLiteral"})
	replacementGen = rapid.SampledFrom([]string{"true", "false", "n >= 0", "{}", `""`, ""})
	settledGen     = rapid.SampledFrom([]string{"Killed", "Survived", "NoCoverage", "CompileError", "RuntimeError", "Timeout", "Ignored"})
	strykerIDGen   = rapid.StringMatching(`[0-9]{1,3}`)
)

func drawMutants(t *rapid.T, file, label string) []genMutant {
	var out []genMutant
	seen := map[mutantKey]bool{}
	for range rapid.IntRange(0, 5).Draw(t, label+"Count") {
		sl := rapid.IntRange(1, 4).Draw(t, label+"StartLine")
		k := mutantKey{
			file: file,
			sl:   sl, sc: rapid.IntRange(1, 4).Draw(t, label+"StartCol"),
			el: sl + rapid.IntRange(0, 2).Draw(t, label+"Span"), ec: rapid.IntRange(1, 4).Draw(t, label+"EndCol"),
			mutator: mutatorGen.Draw(t, label+"Mutator"), replacement: replacementGen.Draw(t, label+"Replacement"),
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, genMutant{key: k, id: strykerIDGen.Draw(t, label+"ID"), status: settledGen.Draw(t, label+"Status")})
		}
	}
	return out
}

func sourceOf(file string) string { return "// source of " + file + "\n" }

// strykerJSON renders mutants the way Stryker's json reporter does, keyed by
// name(file) and carrying fields the adapter does not read.
func strykerJSON(t fataler, ms []genMutant, name func(string) string) []byte {
	files := map[string]map[string]any{}
	for _, m := range ms {
		n := name(m.key.file)
		if files[n] == nil {
			files[n] = map[string]any{"language": "typescript", "source": sourceOf(m.key.file), "mutants": []any{}}
		}
		mut := map[string]any{
			"id": m.id, "mutatorName": m.key.mutator, "status": m.status, "coveredBy": []string{"0"}, "static": false,
			"location": map[string]any{
				"start": map[string]int{"line": m.key.sl, "column": m.key.sc},
				"end":   map[string]int{"line": m.key.el, "column": m.key.ec},
			},
		}
		if m.key.replacement != "" {
			mut["replacement"] = m.key.replacement
		}
		files[n]["mutants"] = append(files[n]["mutants"].([]any), mut)
	}
	data, err := json.Marshal(map[string]any{"schemaVersion": "1", "thresholds": map[string]int{"high": 80, "low": 60}, "files": files})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return data
}

func keyOf(file string, m strykerMutant) mutantKey {
	s, e := m.Location.Start, m.Location.End
	return mutantKey{file, s.Line, s.Column, e.Line, e.Column, m.MutatorName, m.Replacement}
}

func identity(n string) (string, error) { return n, nil }

var mutationUnits = []string{"packages/a", "packages/b"}

func drawUnitMutants(t *rapid.T) (perUnit map[string][]genMutant, all []genMutant) {
	perUnit = map[string][]genMutant{}
	for _, name := range []string{"src/x.ts", "src/y.ts", "lib/z.ts"} {
		if !rapid.Bool().Draw(t, "has "+name) {
			continue
		}
		unit := rapid.SampledFrom(mutationUnits).Draw(t, "unit of "+name)
		ms := drawMutants(t, unit+"/"+name, name)
		if len(ms) == 0 {
			ms = []genMutant{{key: mutantKey{unit + "/" + name, 1, 1, 1, 2, "BlockStatement", "{}"}, id: "0", status: "Killed"}}
		}
		perUnit[unit] = append(perUnit[unit], ms...)
		all = append(all, ms...)
	}
	return perUnit, all
}

func mutantIDs(t fataler, r strykerReport) map[mutantKey]string {
	got := map[mutantKey]string{}
	seen := map[string]bool{}
	for p, f := range r.Files {
		if f.Language != "typescript" || f.Source != sourceOf(p) {
			t.Fatalf("file %s language %q source %q; the report keeps each file's source under its repo-relative path", p, f.Language, f.Source)
		}
		for _, m := range f.Mutants {
			if seen[m.ID] {
				t.Fatalf("id %s given to two mutants; ids must be unique for distinct file, position, mutator and replacement", m.ID)
			}
			seen[m.ID] = true
			got[keyOf(p, m)] = m.ID
		}
	}
	return got
}

func assureReads(t fataler, r strykerReport) []string {
	data, err := r.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	parsed, err := ev.ParseMutationReport(data)
	if err != nil {
		t.Fatalf("assure rejects the merged report: %v\n%s", err, data)
	}
	var out []string
	for _, m := range parsed {
		out = append(out, fmt.Sprintf("%s:%d %s %s", m.Path, m.Line, m.Mutator, m.Status))
	}
	slices.Sort(out)
	return out
}

func TestStrykerReportIDsDependOnlyOnMutant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		perUnit, all := drawUnitMutants(t)
		merged := newStrykerReport()
		for _, unit := range mutationUnits {
			if len(perUnit[unit]) == 0 {
				continue
			}
			data := strykerJSON(t, perUnit[unit], func(f string) string { return strings.TrimPrefix(f, unit+"/") })
			if err := merged.addStryker(data, func(n string) (string, error) { return unit + "/" + n, nil }); err != nil {
				t.Fatalf("addStryker for %s: %v", unit, err)
			}
		}

		reordered := slices.Clone(all)
		for i := range reordered {
			reordered[i].id = strykerIDGen.Draw(t, "otherID")
			reordered[i].status = settledGen.Draw(t, "otherStatus")
		}
		reordered = rapid.Permutation(reordered).Draw(t, "order")
		single := newStrykerReport()
		if err := single.addStryker(strykerJSON(t, reordered, func(f string) string { return f }), identity); err != nil {
			t.Fatalf("addStryker of the same mutants in one report: %v", err)
		}

		a, b := mutantIDs(t, merged), mutantIDs(t, single)
		var wantRead []string
		for _, m := range all {
			if _, ok := a[m.key]; !ok {
				t.Fatalf("merged report has mutants %v, lacking %+v; every unit's mutants appear once under its repo-relative path", slices.Collect(maps.Keys(a)), m.key)
			}
			wantRead = append(wantRead, fmt.Sprintf("%s:%d %s %s", m.key.file, m.key.sl, m.key.mutator, m.status))
		}
		if len(a) != len(all) {
			t.Fatalf("merged report has %d mutants, want %d", len(a), len(all))
		}
		for k, id := range a {
			if b[k] != id {
				t.Fatalf("mutant %+v has id %q from per-unit reports and %q from one reordered report with other Stryker ids and statuses; the id must depend only on file, position, mutator and replacement so a mutant keeps its identity across runs", k, id, b[k])
			}
		}
		slices.Sort(wantRead)
		if got := assureReads(t, merged); !slices.Equal(got, wantRead) {
			t.Fatalf("assure reads %q, want %q; status and position must survive the merge unchanged", got, wantRead)
		}
	})
}

var errOutside = errors.New("outside the repository")

func TestStrykerReportRejectsAmbiguity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ms := drawMutants(t, "src/x.ts", "x")
		ms = append(ms, genMutant{key: mutantKey{"src/x.ts", 9, 1, 9, 5, "EqualityOperator", "n >= 0"}, id: "9", status: "Survived"})
		ms = append(ms, drawMutants(t, "src/y.ts", "y")...)
		fault := rapid.SampledFrom([]string{"pending", "duplicate", "outside", "twice"}).Draw(t, "fault")
		i := rapid.IntRange(0, len(ms)-1).Draw(t, "victim")
		rel := identity
		var want string
		switch fault {
		case "pending":
			ms[i].status = "Pending"
			want = "Pending"
		case "duplicate":
			dup := ms[i]
			dup.id = strykerIDGen.Draw(t, "dupID")
			dup.status = settledGen.Draw(t, "dupStatus")
			ms = rapid.Permutation(append(ms, dup)).Draw(t, "order")
			want = "two mutants"
		case "outside":
			bad := ms[i].key.file
			rel = func(n string) (string, error) {
				if n == bad {
					return "", errOutside
				}
				return n, nil
			}
		case "twice":
			want = "two units"
		}
		data := strykerJSON(t, ms, func(f string) string { return f })
		r := newStrykerReport()
		var err error
		if fault == "twice" {
			if err = r.addStryker(strykerJSON(t, ms[i:i+1], func(f string) string { return f }), identity); err != nil {
				t.Fatalf("first unit: %v", err)
			}
		}
		err = r.addStryker(data, rel)
		switch {
		case err == nil:
			t.Fatalf("%s: no error; a Pending mutant, two mutants with one id, a path outside the repository, or one file from two units each make the report ambiguous and must fail closed", fault)
		case fault == "outside" && !errors.Is(err, errOutside):
			t.Fatalf("error %v does not wrap the path error", err)
		case want != "" && !strings.Contains(err.Error(), want):
			t.Fatalf("%s: error %q does not say %q", fault, err, want)
		}
	})
}

// drawEdit returns old and new file lines and the 1-based new-file lines the
// edit added or replaced. Every line is unique, so git's minimal diff keeps
// exactly the kept lines and the changed set has one correct answer.
func drawEdit(t *rapid.T) (oldLines, newLines []string, changed []int) {
	mimic := func(label string) string {
		return rapid.SampledFrom([]string{"", "@@ -1 +99 @@ ", "@@ -1,2 +3,4 @@ "}).Draw(t, label)
	}
	fresh := 0
	add := func(prefix string) {
		newLines = append(newLines, fmt.Sprintf("%sn%d", prefix, fresh))
		fresh++
		changed = append(changed, len(newLines))
	}
	insert := func() {
		for range rapid.IntRange(0, 2).Draw(t, "inserted") {
			add(mimic("newPrefix"))
		}
	}
	for i := range rapid.IntRange(0, 8).Draw(t, "oldLines") {
		line := fmt.Sprintf("%so%d", mimic("oldPrefix"), i)
		oldLines = append(oldLines, line)
		insert()
		switch rapid.SampledFrom([]string{"keep", "delete", "replace"}).Draw(t, "op") {
		case "keep":
			newLines = append(newLines, line)
		case "replace":
			add("r")
		}
	}
	insert()
	return oldLines, newLines, changed
}

func coveredLines(t fataler, entries []string, path string) []int {
	var covered []int
	for _, e := range entries {
		file, span, ok := strings.Cut(e, ":")
		a, b, ok2 := strings.Cut(span, "-")
		start, err1 := strconv.Atoi(a)
		end, err2 := strconv.Atoi(b)
		if !ok || !ok2 || err1 != nil || err2 != nil || file != path || start < 1 || end < start {
			t.Fatalf("mutate entry %q is not %s:<start>-<end> with 1 <= start <= end", e, path)
		}
		for l := start; l <= end; l++ {
			if slices.Contains(covered, l) {
				t.Fatalf("entries %q cover line %d twice", entries, l)
			}
			covered = append(covered, l)
		}
	}
	slices.Sort(covered)
	return covered
}

func TestMutateEntriesAreChangedLines(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{"package.json": "{}"})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	t.Chdir(top)
	iter := 0
	rapid.Check(t, func(t *rapid.T) {
		iter++
		kind := rapid.SampledFrom([]string{"source", "generated-header", "declaration", "test", "untracked"}).Draw(t, "kind")
		path := fmt.Sprintf("src/f%d.ts", iter)
		header := ""
		switch kind {
		case "generated-header":
			header = "// Code generated by gen. DO NOT EDIT.\n"
		case "declaration":
			path = fmt.Sprintf("src/f%d.d.ts", iter)
		case "test":
			path = fmt.Sprintf("src/f%d.test.ts", iter)
		}
		oldLines, newLines, changed := drawEdit(t)
		text := func(lines []string) string {
			if len(lines) == 0 {
				return header
			}
			return header + strings.Join(lines, "\n") + "\n"
		}
		if kind != "untracked" {
			writeTree(t, top, map[string]string{path: text(oldLines)})
			git(t, top, "add", "-A")
			git(t, top, "commit", "-q", "--allow-empty", "-m", path)
		}
		writeTree(t, top, map[string]string{path: text(newLines)})

		got, err := mutateEntries(runContext{root: top, ref: "HEAD", sel: map[string][]string{top: {path}}}, top)
		if err != nil {
			t.Fatalf("mutateEntries: %v", err)
		}
		if kind != "source" {
			if len(got) != 0 {
				t.Fatalf("%s file %s gave mutate entries %q; only tracked, non-generated source files are mutated", kind, path, got)
			}
			return
		}
		offset := strings.Count(header, "\n")
		covered := coveredLines(t, got, path)
		want := make([]int, 0, len(changed))
		for _, l := range changed {
			want = append(want, l+offset)
		}
		if !slices.Equal(covered, want) {
			t.Fatalf("old %q\nnew %q\nentries %q cover lines %v, want exactly the added or replaced lines %v; a missed line leaves changed code unmutated, an extra line mutates code the change did not touch", oldLines, newLines, got, covered, want)
		}
	})
}

const fakeStrykerVersion = "10.0.0"

func installFakeStryker(t fataler, dir, data string) {
	installTool(t, dir, "@stryker-mutator/core", fakeStrykerVersion)
	installTool(t, dir, "@stryker-mutator/vitest-runner", fakeStrykerVersion)
	d := filepath.Join(data, "stryker")
	exe(t, filepath.Join(dir, "node_modules", ".bin", "stryker"), fmt.Sprintf(`#!/bin/sh
d='%s'
pwd -P > "$d.cwd"
printf '%%s\n' "$@" > "$d.args"
printf '%%s' "$ASSURE_SEED" > "$d.seed"
/bin/cat "$2" > "$d.config"
[ -f "$d.report" ] && /bin/cat "$d.report" > "${2%%/*}/report.json"
exit $(/bin/cat "$d.code" 2>/dev/null || echo 0)
`, d))
}

type strykerCall struct {
	call
	seed   string
	config struct {
		TestRunner   string            `json:"testRunner"`
		Plugins      []string          `json:"plugins"`
		Reporters    []string          `json:"reporters"`
		Mutate       []string          `json:"mutate"`
		Thresholds   map[string]any    `json:"thresholds"`
		TempDirName  string            `json:"tempDirName"`
		JSONReporter map[string]string `json:"jsonReporter"`
	}
}

func strykerCallOf(t fataler, data string) strykerCall {
	c := strykerCall{call: callOf(t, data, "stryker")}
	if !c.invoked {
		return c
	}
	d := filepath.Join(data, "stryker")
	seed, err := os.ReadFile(d + ".seed")
	if err != nil {
		t.Fatalf("%v", err)
	}
	c.seed = string(seed)
	cfg, err := os.ReadFile(d + ".config")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := json.Unmarshal(cfg, &c.config); err != nil {
		t.Fatalf("stryker config %s: %v", cfg, err)
	}
	return c
}

func mutationIn(t fataler, out string, resp runResponse) (strykerReport, []ev.Mutant) {
	if want := []evidence{{Type: "mutation.report", Path: "mutation.json"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v; VER-MUTATION-CHANGED reads exactly this entry", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "mutation.json"))
	if err != nil {
		t.Fatalf("evidence names mutation.json but it was not written: %v", err)
	}
	ms, err := ev.ParseMutationReport(data)
	if err != nil {
		t.Fatalf("assure rejects the mutation report: %v\n%s", err, data)
	}
	var rep strykerReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("%v", err)
	}
	if rep.Files == nil {
		t.Fatalf("report %s has no files object", data)
	}
	return rep, ms
}

func TestRunMutationNoChangedTypeScriptFiles(t *testing.T) {
	r := newRepo(t, nil, nil)
	writeTree(t, r.top, map[string]string{"README.md": "changed\n", "notes.txt": "new\n"})
	out := t.TempDir()
	resp := runIn(t, "VER-MUTATION-CHANGED", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q with no TypeScript change; Stryker is not installed but nothing needs it", resp.Environment.Message)
	}
	if want := map[string]string{"node": fakeNodeVersion}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v; with no unit selected no tool is resolved", resp.ToolVersions)
	}
	if rep, ms := mutationIn(t, out, resp); len(rep.Files) != 0 || len(ms) != 0 {
		t.Fatalf("report files %v; only non-TypeScript files changed, so the spec wants a report with no files", rep.Files)
	}
}

type strykerRepo struct{ top, unit, unitData string }

// newStrykerRepo commits a unit at packages/a, so Stryker's unit-relative
// paths differ from the repo-relative paths assure reads, with a fake Stryker.
func newStrykerRepo(t *testing.T) strykerRepo {
	isolateGit(t)
	fakeNodeOnPath(t)
	top, data := realTempDir(t), t.TempDir()
	r := strykerRepo{top: top, unit: filepath.Join(top, "packages", "a"), unitData: filepath.Join(data, "packages", "a")}
	buildRepo(t, top, data, map[string]string{
		".gitignore":                   "node_modules\n",
		"packages/a/package.json":      "{}",
		"packages/a/package-lock.json": "{}",
		"packages/a/src/x.ts":          "export function f(n: number): boolean {\n  return n > 1;\n}\n",
		"packages/a/src/x.test.ts":     "",
		"packages/a/src/gen.ts":        "// Code generated by gen. DO NOT EDIT.\nexport const g = 1;\n",
	}, map[string]map[string]string{"packages/a": {"vitest": "5.0.4"}})
	installFakeStryker(t, r.unit, r.unitData)
	t.Chdir(top)
	return r
}

func (r strykerRepo) changeSource(t *testing.T) {
	writeTree(t, r.top, map[string]string{
		"packages/a/src/x.ts":   "export function f(n: number): boolean {\n  return n > 0;\n}\n",
		"packages/a/src/gen.ts": "// Code generated by gen. DO NOT EDIT.\nexport const g = 2;\n",
		"packages/a/src/new.ts": "export const fresh = 1;\n",
	})
}

func TestRunMutationSkipsUnitWithNoChangedSourceLine(t *testing.T) {
	r := newStrykerRepo(t)
	writeTree(t, r.top, map[string]string{"packages/a/src/x.test.ts": "changed"})
	out := t.TempDir()
	rep, _ := mutationIn(t, out, runIn(t, "VER-MUTATION-CHANGED", out))
	if strykerCallOf(t, r.unitData).invoked || len(rep.Files) != 0 {
		t.Fatalf("Stryker ran or files %v with only a test file changed; Stryker with an empty mutate list mutates the whole unit", rep.Files)
	}
}

func TestRunMutationRunsStrykerOnChangedLines(t *testing.T) {
	r := newStrykerRepo(t)
	r.changeSource(t)
	replay(t, r.unitData, "stryker", replayed{report: `{"schemaVersion":"1","thresholds":{"high":80,"low":60},"files":{"src/x.ts":{"language":"typescript","source":"S","mutants":[
		{"id":"0","mutatorName":"EqualityOperator","replacement":"n >= 0","status":"Survived","location":{"start":{"line":2,"column":10},"end":{"line":2,"column":15}}},
		{"id":"1","mutatorName":"BooleanLiteral","status":"Killed","location":{"start":{"line":2,"column":10},"end":{"line":2,"column":15}}}]}}}`})
	out := t.TempDir()
	resp := runIn(t, "VER-MUTATION-CHANGED", out)
	if want := map[string]string{"node": fakeNodeVersion, "vitest": "5.0.4", "@stryker-mutator/core": fakeStrykerVersion, "@stryker-mutator/vitest-runner": fakeStrykerVersion}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v", resp.ToolVersions, want)
	}
	rep, ms := mutationIn(t, out, resp)
	if got := slices.Collect(maps.Keys(rep.Files)); !slices.Equal(got, []string{"packages/a/src/x.ts"}) {
		t.Fatalf("files %v; Stryker names files relative to the unit and assure needs them relative to the repository", got)
	}
	if len(ms) != 2 || !slices.ContainsFunc(ms, func(m ev.Mutant) bool { return m.Status == "Survived" && m.Line == 2 }) {
		t.Fatalf("mutants %+v; both of Stryker's mutants must pass through with their status", ms)
	}

	c := strykerCallOf(t, r.unitData)
	if c.cwd != r.unit {
		t.Fatalf("stryker ran in %s, want the unit %s", c.cwd, r.unit)
	}
	if !slices.Equal(c.config.Mutate, []string{"src/x.ts:2-2"}) {
		t.Fatalf("mutate %q, want only the changed line of src/x.ts; the generated src/gen.ts, the untracked src/new.ts and the test file are never mutated", c.config.Mutate)
	}
	sha, err := gitOutput(r.top, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if seed, _ := seedFrom(sha); c.seed != seed {
		t.Fatalf("ASSURE_SEED %q, want %q from HEAD; the run must be reproducible", c.seed, seed)
	}
	checkStrykerConfig(t, c, r.top)
}

func checkStrykerConfig(t *testing.T, c strykerCall, top string) {
	if c.config.TestRunner != "vitest" || !slices.Contains(c.config.Plugins, "@stryker-mutator/vitest-runner") || !slices.Equal(c.config.Reporters, []string{"json"}) {
		t.Fatalf("config %+v; the spec runs Stryker with the vitest runner and reads its json report", c.config)
	}
	if b, ok := c.config.Thresholds["break"]; !ok || b != nil {
		t.Fatalf("thresholds %v; a break threshold would turn a low score into a Stryker failure, but assure judges the score", c.config.Thresholds)
	}
	if strings.HasPrefix(c.config.TempDirName, top) || strings.HasPrefix(c.config.JSONReporter["fileName"], top) {
		t.Fatalf("sandbox %s or report %s inside the repository; the run must leave the worktree untouched", c.config.TempDirName, c.config.JSONReporter["fileName"])
	}
}

func TestRunMutationStrykerFailureIsError(t *testing.T) {
	r := newStrykerRepo(t)
	r.changeSource(t)
	for _, bad := range []replayed{
		{report: `{"files":{"src/x.ts":{"language":"typescript","source":"S","mutants":[]}}}`, code: 1},
		{report: `{"files":{"src/x.ts":{"language":"typescript","source":"S","mutants":[{"id":"0","mutatorName":"BooleanLiteral","status":"Pending","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":2}}}]}}}`},
		{report: `{"files":{"../../../outside.ts":{"language":"typescript","source":"S","mutants":[]}}}`},
		{noReport: true},
	} {
		replay(t, r.unitData, "stryker", bad)
		out := t.TempDir()
		code, stdout, stderr := runRaw("VER-MUTATION-CHANGED", out)
		if code != 1 || len(stdout) != 0 || exists(filepath.Join(out, "mutation.json")) {
			t.Fatalf("stryker exit %d report %q: run exited %d stdout %q stderr %q; a failed Stryker run, a Pending mutant, a file outside the repository or a missing report is an error, never evidence", bad.code, bad.report, code, stdout, stderr)
		}
	}
}
