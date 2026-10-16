package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func junitOf(t fataler, suites []xmlSuite) ev.JUnit {
	data, err := xml.MarshalIndent(xmlSuites{Suites: suites}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j, err := ev.ParseJUnit(append([]byte(xml.Header), data...))
	if err != nil {
		t.Fatalf("assure's JUnit reader rejects the converted evidence, so VER-TESTS-PASS would fail on every run: %v\n%s", err, data)
	}
	return j
}

var (
	specSkipStatuses = []string{"skipped", "pending", "todo", "disabled"}
	vitestStatuses   = append([]string{"passed", "failed"}, specSkipStatuses...)
	xmlTextGen       = rapid.StringMatching(`[ -~\t\n]{0,40}`)
	titleGen         = rapid.StringMatching(`[a-zA-Z0-9 >_.-]{0,8}`)
)

var unknownStatusGen = rapid.Custom(func(t *rapid.T) string {
	s := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "status")
	if slices.Contains(vitestStatuses, s) {
		return s + "x"
	}
	return s
})

// specText is the error text the spec promises: the tool's output, trimmed,
// and never empty, so a reader of the evidence always sees something.
func specText(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "no output"
	}
	return s
}

type wantCase struct {
	name    string
	outcome ev.Outcome
	text    string
}

type wantSuite struct {
	name  string
	cases []wantCase
}

func drawVitestReport(t *rapid.T, base string) (vitestReport, []wantSuite) {
	var r vitestReport
	var want []wantSuite
	used := map[string]bool{}
	for range rapid.IntRange(0, 4).Draw(t, "nfiles") {
		rel := rapid.StringMatching(`(src/)?[a-z]{1,4}(/[a-z]{1,4})?\.(test|spec)\.ts`).Draw(t, "file")
		if used[rel] {
			continue
		}
		used[rel] = true
		writeTree(t, base, map[string]string{rel: ""})
		f := vitestFile{
			Name:    base + "/" + rel,
			Status:  rapid.SampledFrom([]string{"passed", "failed", "skipped"}).Draw(t, "fileStatus"),
			Message: xmlTextGen.Draw(t, "fileMessage"),
		}
		s := wantSuite{name: rel}
		failing := false
		for range rapid.IntRange(0, 4).Draw(t, "ntests") {
			a := vitestAssertion{
				AncestorTitles:  rapid.SliceOfN(titleGen, 0, 3).Draw(t, "ancestors"),
				Title:           titleGen.Draw(t, "title"),
				Status:          rapid.OneOf(rapid.SampledFrom(vitestStatuses), unknownStatusGen).Draw(t, "status"),
				FailureMessages: rapid.SliceOfN(xmlTextGen, 0, 2).Draw(t, "failures"),
			}
			f.AssertionResults = append(f.AssertionResults, a)
			c := wantCase{name: rel + "." + strings.Join(append(slices.Clone(a.AncestorTitles), a.Title), " > ")}
			switch {
			case a.Status == "passed":
				c.outcome = ev.Passed
			case slices.Contains(specSkipStatuses, a.Status):
				c.outcome = ev.Skipped
			case a.Status == "failed":
				c.outcome, c.text, failing = ev.Failed, specText(strings.Join(a.FailureMessages, "\n")), true
			default:
				c.outcome, failing = ev.Errored, true
			}
			s.cases = append(s.cases, c)
		}
		if f.Status == "failed" && !failing {
			s.cases = append(s.cases, wantCase{name: rel + ".(file)", outcome: ev.Errored, text: specText(f.Message)})
		}
		r.TestResults = append(r.TestResults, f)
		want = append(want, s)
	}
	return r, want
}

func TestConversionPreservesFailures(t *testing.T) {
	unreal := t.TempDir()
	root := realPath(unreal)
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.SampledFrom([]string{root, unreal}).Draw(t, "base")
		r, want := drawVitestReport(t, base)
		j := junitOf(t, toJUnit(root, r))
		failedTests, failedFiles := specFailures(r)
		if got := j.Failures + j.Errors; got != failedTests+failedFiles {
			t.Fatalf("failures %d + errors %d = %d, want %d failed (or unrecognised) tests + %d files failed without a failing test; a lost failure lets VER-TESTS-PASS pass a red run", j.Failures, j.Errors, got, failedTests, failedFiles)
		}
		checkSuites(t, j, want)
	})
}

// specFailures counts what the spec says must fail: failed tests (and, failing
// closed, tests with a status vitest does not document) plus test files that
// failed without any such test.
func specFailures(r vitestReport) (tests, files int) {
	for _, f := range r.TestResults {
		failing := false
		for _, a := range f.AssertionResults {
			if a.Status == "failed" || !slices.Contains(vitestStatuses, a.Status) {
				tests++
				failing = true
			}
		}
		if f.Status == "failed" && !failing {
			files++
		}
	}
	return tests, files
}

func checkSuites(t fataler, j ev.JUnit, want []wantSuite) {
	if len(j.Suites) != len(want) {
		t.Fatalf("%d suites, want one per vitest file (%d)", len(j.Suites), len(want))
	}
	failing := j.Failing
	for i, ws := range want {
		gs := j.Suites[i]
		if gs.Name != ws.name {
			t.Fatalf("suite %d named %q, want the repo-relative file %q; assure reports failures by path, and an absolute temp path names nothing in the repo", i, gs.Name, ws.name)
		}
		if len(gs.Cases) != len(ws.cases) {
			t.Fatalf("suite %s has %d cases, want %d: %+v", ws.name, len(gs.Cases), len(ws.cases), gs.Cases)
		}
		for k, wc := range ws.cases {
			if gc := gs.Cases[k]; gc.Name != wc.name || gc.Outcome != wc.outcome {
				t.Fatalf("case %d of %s = %q %s, want %q %s; names join ancestorTitles and title with \" > \" so a fail-on-base or failure report names the test the developer wrote", k, ws.name, gc.Name, gc.Outcome, wc.name, wc.outcome)
			}
			if wc.outcome != ev.Failed && wc.outcome != ev.Errored {
				continue
			}
			if len(failing) == 0 || failing[0].Name != wc.name {
				t.Fatalf("failing case %q missing at its position in the failure list %+v", wc.name, j.Failing)
			}
			if got := failing[0].Text; wc.text != "" && got != wc.text {
				t.Fatalf("case %d of %s (%q) text %q, want %q; the failing case must carry the tool's message, or the developer sees a failure with no reason", k, ws.name, wc.name, got, wc.text)
			}
			failing = failing[1:]
		}
	}
	if len(failing) != 0 {
		t.Fatalf("extra failing cases %+v that no vitest result accounts for", failing)
	}
}

func TestCapTextKeepsATrimmedPrefix(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		if rapid.Bool().Draw(t, "long") {
			s = strings.Repeat(s+"x", rapid.IntRange(maxOutput/2, maxOutput*2).Draw(t, "n")/(len(s)+1)+1)
		}
		got, trimmed := capText([]byte(s)), strings.TrimSpace(s)
		if trimmed == "" {
			if got != "no output" {
				t.Fatalf("capText(blank) = %q, want \"no output\"; an empty error text reads as no error at all", got)
			}
			return
		}
		if want := trimmed[:min(len(trimmed), 16<<10)]; got != want {
			t.Fatalf("capText = %d bytes, want the first %d bytes of the trimmed output; tool output is capped at 16 KiB so one runaway test cannot bloat the evidence, and below the cap nothing is lost", len(got), len(want))
		}
	})
}

func drawReportFile(t *rapid.T, dir, root, state string) (string, []wantSuite) {
	f, err := os.CreateTemp(dir, "report-*.json")
	if err != nil {
		t.Fatalf("%v", err)
	}
	report := f.Name()
	_ = f.Close()
	var data []byte
	var want []wantSuite
	switch state {
	case "missing":
		if err := os.Remove(report); err != nil {
			t.Fatalf("%v", err)
		}
		return report, nil
	case "garbage":
		data = []byte(rapid.SampledFrom([]string{"", "{", "[]", "not json", `{"testResults":{}}`}).Draw(t, "garbage"))
	default:
		var r vitestReport
		r, want = drawVitestReport(t, root)
		if data, err = json.Marshal(r); err != nil {
			t.Fatalf("%v", err)
		}
	}
	if err := os.WriteFile(report, data, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	return report, want
}

func anyFailing(want []wantSuite) bool {
	for _, s := range want {
		for _, c := range s.cases {
			if c.outcome == ev.Failed || c.outcome == ev.Errored {
				return true
			}
		}
	}
	return false
}

func TestVitestSuitesFailClosed(t *testing.T) {
	root := realTempDir(t)
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		output := xmlTextGen.Draw(t, "output")
		code := rapid.IntRange(0, 3).Draw(t, "code")
		state := rapid.SampledFrom([]string{"missing", "garbage", "valid"}).Draw(t, "report")
		report, want := drawReportFile(t, dir, root, state)
		j := junitOf(t, vitestSuites(root, "packages/api", report, []byte(output), code))

		extra := state != "valid" || (code != 0 && !anyFailing(want))
		wantSuites := len(want)
		if extra {
			wantSuites++
		}
		if len(j.Suites) != wantSuites {
			t.Fatalf("report %s, exit %d: %d suites, want %d; a missing or unreadable report, or a non-zero exit with no failing case, must add an erroring suite, or VER-TESTS-PASS passes a run that never reported", state, code, len(j.Suites), wantSuites)
		}
		if !extra {
			return
		}
		last := j.Suites[len(j.Suites)-1]
		if !strings.Contains(last.Name, "packages/api") || len(last.Cases) != 1 || last.Cases[0].Outcome != ev.Errored {
			t.Fatalf("report %s, exit %d: last suite %+v, want one erroring case naming unit packages/api", state, code, last)
		}
		if got := j.Failing[len(j.Failing)-1]; got.Text != specText(output) {
			t.Fatalf("error text %q, want the tool output %q; without it the developer cannot tell why vitest did not report", got.Text, specText(output))
		}
	})
}

func writeReport(t *rapid.T, dir string, r vitestReport) string {
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("%v", err)
	}
	f, err := os.CreateTemp(dir, "report-*.json")
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("%v", err)
	}
	return f.Name()
}

func runSuites(suites []xmlSuite) int {
	n := 0
	for _, s := range suites {
		for _, c := range s.Cases {
			if c.Name == "(run)" && c.Error != nil {
				n++
			}
		}
	}
	return n
}

func TestVitestNonZeroExitNeedsAFailingCaseToExplainIt(t *testing.T) {
	root := realTempDir(t)
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		var r vitestReport
		for i := range rapid.IntRange(1, 3).Draw(t, "nfiles") {
			f := vitestFile{Name: filepath.Join(root, "f"+strings.Repeat("x", i)+".test.ts"), Status: rapid.SampledFrom([]string{"passed", "skipped"}).Draw(t, "fileStatus")}
			for range rapid.IntRange(1, 4).Draw(t, "ntests") {
				f.AssertionResults = append(f.AssertionResults, vitestAssertion{
					Title:  titleGen.Draw(t, "title"),
					Status: rapid.SampledFrom(append([]string{"passed"}, specSkipStatuses...)).Draw(t, "status"),
				})
			}
			r.TestResults = append(r.TestResults, f)
		}
		code := rapid.IntRange(1, 255).Draw(t, "code")
		output := xmlTextGen.Draw(t, "output")

		if got := runSuites(vitestSuites(root, "packages/api", writeReport(t, dir, r), []byte(output), code)); got != 1 {
			t.Fatalf("exit %d with every case passed or skipped: %d (run) error suites, want 1; vitest exiting non-zero with no failing case means something went wrong outside the tests (a crash, a config error, a coverage threshold), and without the (run) suite VER-TESTS-PASS passes a run vitest itself called red", code, got)
		}

		fi := rapid.IntRange(0, len(r.TestResults)-1).Draw(t, "failFile")
		at := rapid.IntRange(0, len(r.TestResults[fi].AssertionResults)).Draw(t, "failAt")
		failed := vitestAssertion{Title: titleGen.Draw(t, "failTitle"), Status: "failed", FailureMessages: rapid.SliceOfN(xmlTextGen, 0, 2).Draw(t, "failures")}
		r.TestResults[fi].AssertionResults = slices.Insert(r.TestResults[fi].AssertionResults, at, failed)

		if got := runSuites(vitestSuites(root, "packages/api", writeReport(t, dir, r), []byte(output), code)); got != 0 {
			t.Fatalf("exit %d with a failing case: %d (run) error suites, want 0; the failing case already explains the non-zero exit, and an extra (run) error would report a failure the developer cannot find in any test", code, got)
		}
	})
}

func TestRelToFollowsSymlinks(t *testing.T) {
	root := realTempDir(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{"src/a.ts": ""})
	for _, p := range []string{filepath.Join(link, "src", "a.ts"), filepath.Join(root, "src", "a.ts")} {
		if got := relTo(root, p); got != "src/a.ts" {
			t.Fatalf("relTo(%s) = %q, want src/a.ts; tools report paths through symlinks (macOS /var is /private/var), and evidence must still name the repo file", p, got)
		}
	}
	if got := relTo(root, "src/a.ts"); got != "src/a.ts" {
		t.Fatalf("relTo of a path that cannot be made relative = %q, want it unchanged with forward slashes", got)
	}
}
