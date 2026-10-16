package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func TestFuzzFileIsSiblingWithAnyExtension(t *testing.T) {
	outer := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp(outer, "root-*")
		if err != nil {
			t.Fatal(err)
		}
		dir := rapid.SampledFrom([]string{"", "src/", "src/deep/"}).Draw(t, "dir")
		stem := rapid.SampledFrom([]string{"parse", "a.b", "fuzz"}).Draw(t, "stem")
		src := dir + stem + "." + rapid.SampledFrom(sourceExts).Draw(t, "srcExt")
		var want []string
		files := map[string]string{src: ""}
		for _, x := range sourceExts {
			if rapid.Bool().Draw(t, "has "+x) {
				f := dir + stem + ".fuzz.test." + x
				files[f] = ""
				want = append(want, f)
			}
		}
		for _, decoy := range []string{
			"other/" + stem + ".fuzz.test.ts",
			dir + stem + ".fuzz.spec.ts",
			dir + stem + ".test.ts",
			dir + stem + "x.fuzz.test.ts",
			dir + stem + ".fuzz.test.js",
			dir + "x" + stem + ".fuzz.test.ts",
		} {
			if rapid.Bool().Draw(t, decoy) {
				files[decoy] = ""
			}
		}
		writeTree(t, root, files)
		got := fuzzFiles(root, src)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("fuzzFiles(%q) = %v with files %v, want %v; the fuzz file is <dir>/<stem>.fuzz.test.<ts|tsx|mts|cts> and nothing else", src, got, files, want)
		}
	})
}

func fuzzIn(t fataler, out string, resp runResponse) ev.JUnit {
	if want := []evidence{{Type: "fuzz.run", Path: "fuzz.xml"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v; VER-ROBUST-FUZZ reads exactly this entry", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "fuzz.xml"))
	if err != nil {
		t.Fatalf("evidence names fuzz.xml but it was not written: %v", err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("assure's fuzz reader rejects the evidence: %v\n%s", err, data)
	}
	return j
}

func fuzzSuites(t fataler, j ev.JUnit) map[string][]ev.Case {
	got := map[string][]ev.Case{}
	for _, s := range j.Suites {
		if len(s.Properties) != 1 || s.Properties[0].Name != "assure.source" {
			t.Fatalf("suite %q properties %+v; each suite carries its source file as its only assure.source property", s.Name, s.Properties)
		}
		got[s.Properties[0].Value] = s.Cases
	}
	return got
}

func TestRunFuzzWithoutFuzzFilesRunsNothing(t *testing.T) {
	r := newRepo(t, nil, nil)
	failOnBaseFake(t, r.top, r.data, r.top, ".")
	installTool(t, r.top, "fast-check", "4.10.2")
	writeTree(t, r.top, map[string]string{"src/parse.ts": "changed", "src/lint.ts": "changed"})
	out := t.TempDir()
	got := fuzzSuites(t, fuzzIn(t, out, runIn(t, "VER-ROBUST-FUZZ", out)))
	if len(got) != 2 || len(got["src/parse.ts"]) != 0 || len(got["src/lint.ts"]) != 0 {
		t.Fatalf("suites %+v; each changed source without a fuzz file has a suite with zero cases", got)
	}
	if c := callOf(t, r.data, "run.head"); c.invoked {
		t.Fatalf("vitest ran with %v; with no fuzz file to run, an unfiltered vitest run would execute the unit's whole suite and report its tests as fuzz cases", c.args)
	}
}

func TestRunFuzzSuitePerChangedSource(t *testing.T) {
	r := newRepo(t, map[string]string{"src/parse.fuzz.test.ts": ""}, nil)
	failOnBaseFake(t, r.top, r.data, r.top, ".")
	installTool(t, r.top, "fast-check", "4.10.2")
	writeTree(t, r.top, map[string]string{"src/parse.ts": "changed", "src/lint.ts": "changed"})
	writeTree(t, r.data, map[string]string{
		"run.head.json": `{"testResults": [{"name": "@ROOT@/src/parse.fuzz.test.ts", "status": "failed", "message": "", "assertionResults": [
  {"ancestorTitles": [], "title": "round trips", "status": "passed", "failureMessages": []},
  {"ancestorTitles": [], "title": "never negative", "status": "failed", "failureMessages": ["Property failed after 3 tests"]}]}]}`,
		"run.head.code": "1",
	})
	out := t.TempDir()
	resp := runIn(t, "VER-ROBUST-FUZZ", out)
	if resp.ToolVersions["fast-check"] != "4.10.2" || resp.ToolVersions["vitest"] != "5.0.4" {
		t.Fatalf("tool_versions %v; the fuzz run uses vitest and fast-check", resp.ToolVersions)
	}
	got := fuzzSuites(t, fuzzIn(t, out, resp))
	want := map[string][]ev.Case{
		"src/lint.ts": nil,
		"src/parse.ts": {
			{Name: "src/parse.fuzz.test.ts.round trips", Outcome: ev.Passed},
			{Name: "src/parse.fuzz.test.ts.never negative", Outcome: ev.Failed},
		},
	}
	if len(got) != len(want) || len(got["src/lint.ts"]) != 0 || !slices.Equal(got["src/parse.ts"], want["src/parse.ts"]) {
		t.Fatalf("suites %+v, want %+v; one suite per changed source, a case per property in its fuzz file, a failing property failed, and zero cases where no fuzz file exists", got, want)
	}
	c := callOf(t, r.data, "run.head")
	if files := c.files(); !slices.Equal(files, []string{filepath.Join(r.top, "src", "parse.fuzz.test.ts")}) {
		t.Fatalf("vitest ran %v; only fuzz files of changed sources run", files)
	}
	if seed, _ := fileText(t, filepath.Join(r.data, "run.head.seed")); seed != checkSeed(t, headOf(t, r.top)) {
		t.Fatalf("ASSURE_SEED %q, want the seed of HEAD; fuzz inputs must replay on the same commit", seed)
	}

	replayNoReport := filepath.Join(r.data, "run.head.json")
	if err := os.Remove(replayNoReport); err != nil {
		t.Fatal(err)
	}
	got = fuzzSuites(t, fuzzIn(t, out, runIn(t, "VER-ROBUST-FUZZ", out)))
	if len(got["src/lint.ts"]) != 0 || len(got["src/parse.ts"]) != 1 || got["src/parse.ts"][0].Outcome != ev.Errored {
		t.Fatalf("suites %+v after vitest wrote no report; a fuzz run that did not report must error in the suites that have fuzz files, never read as zero failing properties", got)
	}
}
