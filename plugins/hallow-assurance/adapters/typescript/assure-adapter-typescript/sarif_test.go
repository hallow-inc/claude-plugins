package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func sarifOf(t fataler, dir string, runs ...sarifRun) []ev.Result {
	out, err := os.MkdirTemp(dir, "sarif-*")
	if err != nil {
		t.Fatalf("%v", err)
	}
	evs, err := writeSARIF(out, runs...)
	if err != nil {
		t.Fatalf("writeSARIF: %v", err)
	}
	if len(evs) != 1 || evs[0] != (evidence{Type: "lint.sarif", Path: "lint.sarif"}) {
		t.Fatalf("evidence %+v, want one lint.sarif; assure looks the file up by this entry", evs)
	}
	data, err := os.ReadFile(filepath.Join(out, "lint.sarif"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	rs, err := ev.ParseSARIF(data)
	if err != nil {
		t.Fatalf("assure rejects the SARIF (vendored 2.1.0 schema), so CODE-ZERO-WARNINGS would fail on every run: %v\n%s", err, data)
	}
	return rs
}

func sameResults(t fataler, what string, got, want []ev.Result) {
	if !slices.Equal(got, want) {
		t.Fatalf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

func TestESLintToSARIFRoundTrip(t *testing.T) {
	unreal := t.TempDir()
	root := realPath(unreal)
	scratch := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.SampledFrom([]string{root, unreal}).Draw(t, "base")
		var files []map[string]any
		want := []ev.Result{}
		for i := range rapid.IntRange(0, 3).Draw(t, "nfiles") {
			rel := fmt.Sprintf("p%d/", i) + rapid.StringMatching(`[a-z]{1,4}(/[a-z]{1,4})?\.(ts|tsx|mts|cts)`).Draw(t, "file")
			writeTree(t, root, map[string]string{rel: ""})
			var msgs []map[string]any
			for range rapid.IntRange(0, 4).Draw(t, "nmsgs") {
				fatal := rapid.Bool().Draw(t, "fatal")
				var rule *string
				if rapid.Bool().Draw(t, "hasRule") {
					r := rapid.StringMatching(`[a-z][a-z@/-]{0,15}`).Draw(t, "rule")
					rule = &r
				}
				sev := rapid.IntRange(1, 2).Draw(t, "severity")
				text := rapid.String().Draw(t, "message")
				m := map[string]any{"ruleId": rule, "severity": sev, "message": text, "line": 1, "column": 1}
				if fatal {
					m["fatal"] = true
				}
				msgs = append(msgs, m)
				w := ev.Result{RuleID: "eslint", Level: "warning", Message: text, URI: rel}
				switch {
				case fatal:
					w.RuleID, w.Level = "parse", "error"
				case rule != nil:
					w.RuleID = *rule
				}
				if !fatal && sev == 2 {
					w.Level = "error"
				}
				want = append(want, w)
			}
			files = append(files, map[string]any{"filePath": filepath.Join(base, filepath.FromSlash(rel)), "messages": msgs, "errorCount": 0})
		}
		stdout, err := json.Marshal(files)
		if err != nil {
			t.Fatalf("%v", err)
		}
		run := newRun("eslint")
		run.Results = eslintResults(root, "packages/api", stdout, nil, true)
		sameResults(t, "ESLint messages to SARIF: fatal is rule parse at error (a file ESLint cannot parse fails closed), null ruleId is eslint, severity 2 is error and 1 warning, URIs repo-relative", sarifOf(t, scratch, run), want)
	})
}

func TestESLintFailureFailsClosed(t *testing.T) {
	scratch := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		ok := rapid.Bool().Draw(t, "exitOK")
		stdout := `[]`
		if ok {
			stdout = rapid.SampledFrom([]string{"", "Oops! Something went wrong!", "{", `{"filePath":"a.ts"}`}).Draw(t, "stdout")
		}
		stderr := xmlTextGen.Draw(t, "stderr")
		run := newRun("eslint")
		run.Results = eslintResults("/r", "packages/api", []byte(stdout), []byte(stderr), ok)
		rs := sarifOf(t, scratch, run)
		if len(rs) != 1 || rs[0].RuleID != "eslint" || rs[0].Level != "error" || rs[0].URI != "" {
			t.Fatalf("eslint crashed (exit ok %v, stdout %q): got %+v, want one error result with rule eslint; an ESLint that did not lint must not read as zero findings", ok, stdout, rs)
		}
		if !strings.Contains(rs[0].Message, "packages/api") || !strings.Contains(rs[0].Message, specText(stderr)) {
			t.Fatalf("message %q must name the unit and carry ESLint's stderr %q, so the developer can fix the configuration", rs[0].Message, stderr)
		}
	})
}

var (
	tscCategories = map[string]string{"error": "error", "warning": "warning", "message": "note"}
	tscMessageGen = rapid.StringMatching(`[a-zA-Z0-9 '.:,]{0,40}`)
	tscNoiseGen   = rapid.SampledFrom([]string{"", "Found 3 errors in 2 files.", "Version 6.0.3", "error TS: no code", "warning TSx: y", "src/a.ts(1): error TS1: no column", "src/a.ts(1,x): error TS1: bad column"})
)

func TestTscToSARIFRoundTrip(t *testing.T) {
	unreal := t.TempDir()
	root := realPath(unreal)
	scratch := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		unitRel := rapid.SampledFrom([]string{".", "packages/api"}).Draw(t, "unit")
		unit := filepath.Join(root, filepath.FromSlash(unitRel))
		var lines []string
		if rapid.Bool().Draw(t, "leadingIndented") {
			lines = append(lines, "  indented text before any diagnostic")
		}
		want := []ev.Result{}
		for range rapid.IntRange(0, 5).Draw(t, "ndiags") {
			cat := rapid.SampledFrom([]string{"error", "warning", "message"}).Draw(t, "category")
			code := "TS" + rapid.StringMatching(`[0-9]{1,5}`).Draw(t, "code")
			msg := tscMessageGen.Draw(t, "message")
			w := ev.Result{RuleID: code, Level: tscCategories[cat], Message: msg}
			switch kind := rapid.SampledFrom([]string{"relative", "absolute", "global"}).Draw(t, "kind"); kind {
			case "global":
				lines = append(lines, fmt.Sprintf("%s %s: %s", cat, code, msg))
			default:
				rel := rapid.StringMatching(`[a-z]{1,4}(/[a-z ]{1,4})?\.(ts|tsx|d\.ts)`).Draw(t, "file")
				writeTree(t, unit, map[string]string{rel: ""})
				file := rel
				if kind == "absolute" {
					file = filepath.Join(rapid.SampledFrom([]string{root, unreal}).Draw(t, "absBase"), filepath.FromSlash(unitRel), filepath.FromSlash(rel))
				}
				lines = append(lines, fmt.Sprintf("%s(%d,%d): %s %s: %s", file, rapid.IntRange(1, 9999).Draw(t, "line"), rapid.IntRange(1, 999).Draw(t, "col"), cat, code, msg))
				w.URI = path.Join(unitRel, rel)
			}
			for range rapid.IntRange(0, 2).Draw(t, "ncont") {
				cont := rapid.StringMatching(`[a-zA-Z'.:,][a-zA-Z0-9 '.:,]{0,30}`).Draw(t, "continuation")
				lines = append(lines, strings.Repeat(" ", rapid.IntRange(1, 6).Draw(t, "indent"))+cont)
				w.Message += "\n" + strings.TrimSpace(cont)
			}
			if rapid.Bool().Draw(t, "noise") {
				lines = append(lines, tscNoiseGen.Draw(t, "noiseLine"))
			}
			want = append(want, w)
		}
		failed := rapid.Bool().Draw(t, "failed")
		output := strings.Join(lines, "\n")
		run := newRun("tsc")
		run.Results = tscResults(root, unit, unitRel, []byte(output), failed)
		got := sarifOf(t, scratch, run)
		if failed && len(want) == 0 {
			if len(got) != 1 || got[0].RuleID != "tsc" || got[0].Level != "error" || !strings.Contains(got[0].Message, unitRel) || !strings.Contains(got[0].Message, specText(output)) {
				t.Fatalf("tsc failed with no diagnostic in %q: got %+v, want one tsc error naming the unit and carrying the output; a tsc that failed without a parsable line must not read as clean", output, got)
			}
			return
		}
		sameResults(t, fmt.Sprintf("tsc output %q to SARIF: every diagnostic, located ones at repo-relative URIs, global ones unlocated, indented lines appended to the message above", output), got, want)
	})
}

func TestTscOverlongLineFailsClosed(t *testing.T) {
	root := realTempDir(t)
	output := "src/a.ts(1,1): error TS1: " + strings.Repeat("x", 17<<20)
	rs := tscResults(root, root, ".", []byte(output), false)
	if len(rs) == 0 || rs[len(rs)-1].RuleID != "tsc" || rs[len(rs)-1].Level != "error" {
		t.Fatalf("a tsc line the scanner cannot read gave %d results ending %+v; unread output must become a tsc error, or a diagnostic is silently dropped", len(rs), rs[max(len(rs)-1, 0):])
	}
}

func TestMergedLintSARIFKeepsBothRuns(t *testing.T) {
	root := realTempDir(t)
	writeTree(t, root, map[string]string{"src/lint.ts": ""})
	eslint, tsc := newRun("eslint"), newRun("tsc")
	stdout, err := json.Marshal([]map[string]any{{"filePath": filepath.Join(root, "src", "lint.ts"), "messages": []map[string]any{{"ruleId": "no-var", "severity": 2, "message": "m"}}}})
	if err != nil {
		t.Fatal(err)
	}
	eslint.Results = eslintResults(root, ".", stdout, nil, true)
	tsc.Results = tscResults(root, root, ".", []byte("src/b.ts(1,1): error TS2322: t\n"), true)
	got := sarifOf(t, t.TempDir(), eslint, tsc)
	want := []ev.Result{
		{RuleID: "no-var", Level: "error", Message: "m", URI: "src/lint.ts"},
		{RuleID: "TS2322", Level: "error", Message: "t", URI: "src/b.ts"},
	}
	sameResults(t, "CODE-ZERO-WARNINGS merges ESLint and tsc into one lint.sarif; dropping either run hides its findings", got, want)
}
