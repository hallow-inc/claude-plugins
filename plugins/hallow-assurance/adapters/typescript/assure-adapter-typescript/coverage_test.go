package main

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func istanbulSpan(start, end int) istanbulRange {
	var r istanbulRange
	r.Start.Line, r.End.Line = start, end
	return r
}

var countGen = rapid.OneOf(rapid.IntRange(0, 3), rapid.IntRange(0, 1<<20))

func drawIstanbul(t *rapid.T) (istanbulFile, map[int]int, []ev.LCOVFunction) {
	f := istanbulFile{StatementMap: map[string]istanbulRange{}, FnMap: map[string]istanbulFunc{}, S: map[string]int{}, F: map[string]int{}}
	lines := map[int]int{}
	for i := range rapid.IntRange(0, 8).Draw(t, "statements") {
		id := strconv.Itoa(i)
		l := rapid.IntRange(1, 6).Draw(t, "line")
		f.StatementMap[id] = istanbulSpan(l, l+rapid.IntRange(0, 2).Draw(t, "stmtSpan"))
		f.S[id] = countGen.Draw(t, "s")
		lines[l] = max(lines[l], min(f.S[id], 1))
	}
	var funcs []ev.LCOVFunction
	for i := range rapid.IntRange(0, 13).Draw(t, "functions") {
		id := strconv.Itoa(i)
		name := rapid.SampledFrom([]string{"decide", "(anonymous_", "parse"}).Draw(t, "name") + id
		start := rapid.IntRange(1, 30).Draw(t, "start")
		end := start + rapid.IntRange(0, 10).Draw(t, "span")
		f.FnMap[id] = istanbulFunc{Name: name, Decl: istanbulSpan(start, start), Loc: istanbulSpan(start, end)}
		f.F[id] = countGen.Draw(t, "f")
		funcs = append(funcs, ev.LCOVFunction{Name: name, Start: start, End: end, Hits: min(f.F[id], 1)})
	}
	return f, lines, funcs
}

func TestLCOVFromIstanbulRoundTrips(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f, lines, funcs := drawIstanbul(t)
		got, err := lcovFile("src/decide.ts", f)
		if err != nil {
			t.Fatalf("lcovFile: %v", err)
		}
		text := ev.FormatLCOV([]ev.LCOVFile{got})
		parsed, err := ev.ParseLCOV(text)
		if err != nil || len(parsed) != 1 {
			t.Fatalf("assure's LCOV reader rejects the evidence (%v):\n%s", err, text)
		}
		p := parsed[0]
		if p.Path != "src/decide.ts" || !maps.Equal(p.Lines, lines) {
			t.Fatalf("SF %q DA %v, want src/decide.ts and %v; one DA per statement start line, hit when any statement there ran", p.Path, p.Lines, lines)
		}
		if !slices.Equal(p.Functions, funcs) {
			t.Fatalf("functions %+v, want %+v; FN spans the declaration's start to the body's end, in numeric id order, with hits kept only as 0 or 1 so the record does not depend on how many inputs a property drew", p.Functions, funcs)
		}
		again, err := lcovFile("src/decide.ts", f)
		if err != nil || string(ev.FormatLCOV([]ev.LCOVFile{again})) != string(text) {
			t.Fatalf("two conversions of one report differ; the coverage evidence must be byte-identical across runs")
		}
	})
}

func TestLCOVFromIstanbulRejectsMalformed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f, _, _ := drawIstanbul(t)
		var what string
		switch rapid.IntRange(0, 3).Draw(t, "defect") {
		case 0:
			what = "a non-numeric function id"
			f.FnMap[rapid.SampledFrom([]string{"x", "1a", "", "-"}).Draw(t, "key")] = istanbulFunc{Name: "g", Decl: istanbulSpan(1, 1), Loc: istanbulSpan(1, 2)}
		case 1:
			what = "a function starting before line 1"
			f.FnMap["99"] = istanbulFunc{Name: "g", Decl: istanbulSpan(rapid.IntRange(-2, 0).Draw(t, "start"), 1), Loc: istanbulSpan(1, 2)}
		case 2:
			what = "a function ending before it starts"
			start := rapid.IntRange(2, 9).Draw(t, "start")
			f.FnMap["99"] = istanbulFunc{Name: "g", Decl: istanbulSpan(start, start), Loc: istanbulSpan(1, start-rapid.IntRange(1, start-1).Draw(t, "back"))}
		case 3:
			what = "a statement starting before line 1"
			f.StatementMap["99"] = istanbulSpan(rapid.IntRange(-2, 0).Draw(t, "line"), 1)
		}
		if got, err := lcovFile("src/decide.ts", f); err == nil {
			t.Fatalf("lcovFile accepted %s and gave %+v; a span the coverage tool cannot have produced must fail the run, not reach assure as evidence", what, got)
		}
	})
}

func coverageReport(entries map[string]string) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		parts = append(parts, fmt.Sprintf("%q: %s", "@ROOT@/"+k, entries[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

const parseCoverage = `{"path": "x",
  "statementMap": {"0": {"start": {"line": 1, "column": 0}, "end": {"line": 3, "column": 1}}, "1": {"start": {"line": 2}, "end": {"line": 2}}, "2": {"start": {"line": 2}, "end": {"line": 2}}},
  "s": {"0": 7, "1": 0, "2": 3},
  "fnMap": {"0": {"name": "parse", "decl": {"start": {"line": 1}, "end": {"line": 1}}, "loc": {"start": {"line": 1}, "end": {"line": 3}}, "line": 1},
            "1": {"name": "(anonymous_1)", "decl": {"start": {"line": 2}, "end": {"line": 2}}, "loc": {"start": {"line": 2}, "end": {"line": 2}}, "line": 2}},
  "f": {"0": 12, "1": 0}, "branchMap": {}, "b": {}}`

func coverageRepo(t *testing.T, report string) tsRepo {
	r := newRepo(t, nil, nil)
	failOnBaseFake(t, r.top, r.data, r.top, ".")
	installTool(t, r.top, "@vitest/coverage-v8", "5.0.4")
	writeTree(t, r.top, map[string]string{"src/parse.ts": "changed"})
	writeTree(t, r.data, map[string]string{"run.head.code": "1"})
	if report != "" {
		writeTree(t, r.data, map[string]string{"coverage.json": report})
	}
	return r
}

func TestRunCoverageWritesLCOVForChangedSources(t *testing.T) {
	r := coverageRepo(t, coverageReport(map[string]string{"src/parse.ts": parseCoverage, "src/lint.ts": parseCoverage}))
	out := t.TempDir()
	resp := runIn(t, "VER-COVERAGE-RESOLUTION", out)
	if want := []evidence{{Type: "coverage.resolution", Path: "coverage.lcov"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v", resp.Evidence, want)
	}
	if resp.ToolVersions["@vitest/coverage-v8"] != "5.0.4" || resp.ToolVersions["vitest"] != "5.0.4" {
		t.Fatalf("tool_versions %v; the coverage run uses vitest and @vitest/coverage-v8", resp.ToolVersions)
	}
	got, _ := fileText(t, filepath.Join(out, "coverage.lcov"))
	want := "SF:src/parse.ts\nFN:1,3,parse\nFN:2,2,(anonymous_1)\nFNDA:1,parse\nFNDA:0,(anonymous_1)\nDA:1,1\nDA:2,1\nend_of_record\n"
	if got != want {
		t.Fatalf("coverage.lcov\n%s\nwant\n%s\nonly the changed src/parse.ts is recorded, with a repo-relative SF, even though vitest exited 1 and reported another file", got, want)
	}
	c := callOf(t, r.data, "run.head")
	var include []string
	for _, a := range c.args {
		if v, ok := strings.CutPrefix(a, "--coverage.include="); ok {
			include = append(include, v)
		}
	}
	if !slices.Equal(include, []string{"src/parse.ts"}) || !slices.Contains(c.args, "--coverage.reportOnFailure") {
		t.Fatalf("vitest args %v; coverage must be restricted to the changed sources and reported even when a test fails", c.args)
	}
	if seed, _ := fileText(t, filepath.Join(r.data, "run.head.seed")); seed != checkSeed(t, headOf(t, r.top)) {
		t.Fatalf("ASSURE_SEED %q, want the seed of HEAD", seed)
	}
}

func TestRunCoverageFailsWithoutCoverageOfChangedSource(t *testing.T) {
	for name, report := range map[string]string{
		"a report without the changed source": coverageReport(map[string]string{"src/lint.ts": parseCoverage}),
		"no report":                           "",
	} {
		coverageRepo(t, report)
		if code, stdout, stderr := runRaw("VER-COVERAGE-RESOLUTION", t.TempDir()); code != 1 || len(stdout) != 0 || !strings.Contains(stderr, "coverage") {
			t.Fatalf("%s: exit %d stdout %q stderr %q; missing coverage must fail the run, not read as an uncovered or absent file", name, code, stdout, stderr)
		}
	}
}
