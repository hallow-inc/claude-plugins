package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const fakeNodeVersion = "v26.8.1"

var recordedDir, _ = filepath.Abs(filepath.Join("testdata", "recorded"))

var fakeBins = map[string]string{"vitest": "vitest", "eslint": "eslint", "typescript": "tsc"}

func exe(t fataler, p, src string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(p, []byte(src), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
}

func fakeNodeOnPath(t *testing.T) {
	bin := t.TempDir()
	exe(t, filepath.Join(bin, "node"), "#!/bin/sh\necho "+fakeNodeVersion+"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// installFake installs pkg at version in dir/node_modules with a .bin script
// that records its argv and working directory into data and replays what
// replay last wrote there.
func installFake(t fataler, dir, data, pkg, version string) {
	installTool(t, dir, pkg, version)
	bin := fakeBins[pkg]
	d := filepath.Join(data, bin)
	exe(t, filepath.Join(dir, "node_modules", ".bin", bin), fmt.Sprintf(`#!/bin/sh
d='%s'
pwd -P > "$d.cwd"
printf '%%s\n' "$@" > "$d.args"
for a; do case "$a" in --outputFile=*) [ -f "$d.report" ] && /bin/cat "$d.report" > "${a#--outputFile=}";; esac; done
[ -f "$d.stdout" ] && /bin/cat "$d.stdout"
[ -f "$d.stderr" ] && /bin/cat "$d.stderr" >&2
exit $(/bin/cat "$d.code" 2>/dev/null || echo 0)
`, d))
}

type replayed struct {
	stdout, stderr, report string
	noReport               bool
	code                   int
}

func replay(t fataler, data, bin string, r replayed) {
	d := filepath.Join(data, bin)
	for _, ext := range []string{".cwd", ".args", ".report"} {
		_ = os.Remove(d + ext)
	}
	files := map[string]string{bin + ".stdout": r.stdout, bin + ".stderr": r.stderr, bin + ".code": fmt.Sprint(r.code)}
	if !r.noReport {
		files[bin+".report"] = r.report
	}
	writeTree(t, data, files)
}

type call struct {
	invoked bool
	args    []string
	cwd     string
}

func callOf(t fataler, data, bin string) call {
	d := filepath.Join(data, bin)
	args, err := os.ReadFile(d + ".args")
	if os.IsNotExist(err) {
		return call{}
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	cwd, err := os.ReadFile(d + ".cwd")
	if err != nil {
		t.Fatalf("%v", err)
	}
	return call{invoked: true, args: strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"), cwd: strings.TrimSpace(string(cwd))}
}

func (c call) files() []string {
	var out []string
	for _, a := range c.args {
		if filepath.IsAbs(a) && hasSourceExt(a) {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

func recorded(t fataler, name, unit string) string {
	data, err := os.ReadFile(filepath.Join(recordedDir, name))
	if err != nil {
		t.Fatalf("%v", err)
	}
	return strings.ReplaceAll(string(data), "@UNIT@", unit)
}

var baseRepo = map[string]string{
	".gitignore":        "node_modules\n",
	"package.json":      "{}",
	"package-lock.json": "{}",
	"tsconfig.json":     "{}",
	"eslint.config.js":  "export default [];\n",
	"README.md":         "readme\n",
	"src/parse.ts":      "export const parse = 1;\n",
	"src/lint.ts":       "export const lint = 1;\n",
	"src/cli.test.ts":   "",
	"src/env.d.ts":      "",
	"src/syntax.ts":     "",
	"src/multi.ts":      "",
	"src/typeerr.ts":    "",
}

// buildRepo writes files and fake tools into top, commits everything, and
// returns top. tools maps a unit directory (relative to top) to the packages
// and versions installed there.
func buildRepo(t fataler, top, data string, files map[string]string, tools map[string]map[string]string) {
	writeTree(t, top, files)
	for unit, pkgs := range tools {
		for pkg, v := range pkgs {
			installFake(t, filepath.Join(top, filepath.FromSlash(unit)), filepath.Join(data, filepath.FromSlash(unit)), pkg, v)
		}
	}
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
}

type tsRepo struct{ top, unreal, data string }

func newRepo(t *testing.T, files map[string]string, tools map[string]map[string]string) tsRepo {
	isolateGit(t)
	fakeNodeOnPath(t)
	unreal := t.TempDir()
	r := tsRepo{top: realPath(unreal), unreal: unreal, data: t.TempDir()}
	all := maps.Clone(baseRepo)
	maps.Copy(all, files)
	buildRepo(t, r.top, r.data, all, tools)
	t.Chdir(r.top)
	return r
}

func runRaw(obj, out string) (int, []byte, string) {
	var o, e bytes.Buffer
	code := run([]string{"run", obj, "--changed-from", "HEAD", "--out", out}, &o, &e)
	return code, o.Bytes(), e.String()
}

func runIn(t fataler, obj, out string) runResponse {
	resp, _ := runInRaw(t, obj, out)
	return resp
}

func runInRaw(t fataler, obj, out string) (runResponse, []byte) {
	code, raw, stderr := runRaw(obj, out)
	if code != 0 {
		t.Fatalf("run %s exited %d: %s; a run that reaches a verdict or an environment problem exits 0", obj, code, stderr)
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, raw); err != nil || len(vs) > 0 {
		t.Fatalf("run %s: response %s fails the adapter-run schema, so assure discards it: %v %v", obj, raw, err, vs)
	}
	var resp runResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v", err)
	}
	protocolIs(t, "run", resp.Protocol, raw)
	return resp, raw
}

// protocolIs reports a wrong protocol without stopping the test, so a test
// that is red for its own reason as well shows both.
func protocolIs(t fataler, op string, got int, raw []byte) {
	if got == 2 {
		return
	}
	msg := fmt.Sprintf("%s response protocol %d, want 2; %s gained the optional untracked marks, and an assure that predates them must refuse the response as a protocol mismatch\n%s", op, got, op, raw)
	if e, ok := t.(interface{ Errorf(string, ...any) }); ok {
		e.Errorf("%s", msg)
		return
	}
	t.Fatalf("%s", msg)
}

func runUntracked(t fataler, raw []byte) ([]string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("%v", err)
	}
	f, ok := fields["untracked"]
	if !ok {
		return nil, false
	}
	var names []string
	if err := json.Unmarshal(f, &names); err != nil {
		t.Fatalf("untracked %s is not a list of tool names: %v", f, err)
	}
	return names, true
}

func junitIn(t fataler, out string, resp runResponse) ev.JUnit {
	if want := []evidence{{Type: "test.junit", Path: "junit.xml"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v; VER-TESTS-PASS reads exactly this entry", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatalf("evidence names junit.xml but it was not written: %v", err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("assure's JUnit reader rejects the evidence: %v\n%s", err, data)
	}
	return j
}

func sarifIn(t fataler, out string, resp runResponse) []ev.Result {
	if want := []evidence{{Type: "lint.sarif", Path: "lint.sarif"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v; CODE-ZERO-WARNINGS reads exactly this entry", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "lint.sarif"))
	if err != nil {
		t.Fatalf("evidence names lint.sarif but it was not written: %v", err)
	}
	rs, err := ev.ParseSARIF(data)
	if err != nil {
		t.Fatalf("assure rejects the SARIF: %v\n%s", err, data)
	}
	return rs
}

func outcomes(j ev.JUnit) map[string]ev.Outcome {
	got := map[string]ev.Outcome{}
	for _, s := range j.Suites {
		for _, c := range s.Cases {
			got[c.Name] = c.Outcome
		}
	}
	return got
}

func failingText(j ev.JUnit, name string) string {
	for _, f := range j.Failing {
		if f.Name == name {
			return f.Text
		}
	}
	return ""
}

func TestRunTestsReplaysRecordedVitest(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
	writeTree(t, r.top, map[string]string{
		"src/parse.ts":       "export const parse = 2;\n",
		"src/broken.test.ts": "import { x } from \"./nope\";\n",
	})
	replay(t, r.data, "vitest", replayed{report: recorded(t, "vitest-report.json", r.unreal), stdout: "JSON report written\n", code: 1})
	out := filepath.Join(t.TempDir(), "nested", "out")

	resp, raw := runInRaw(t, "VER-TESTS-PASS", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q; eslint, typescript and Stryker are not installed but VER-TESTS-PASS uses only vitest, and an unused tool must not block it", resp.Environment.Message)
	}
	if u, ok := runUntracked(t, raw); ok {
		t.Fatalf("untracked %v with a tracked lockfile; absent means every pin is tracked, and a spurious mark fails the objective under authority: ci", u)
	}
	if want := map[string]string{"node": fakeNodeVersion, "vitest": "5.0.4"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v; they record node and exactly the tools this objective ran", resp.ToolVersions, want)
	}
	if want := map[string]string{"vitest": "package-lock.json"}; !maps.Equal(resp.PinnedBy, want) {
		t.Fatalf("pinned_by %v, want %v; node is not pinned by the lockfile", resp.PinnedBy, want)
	}

	j := junitIn(t, out, resp)
	want := map[string]ev.Outcome{
		"src/broken.test.ts.(file)":          ev.Errored,
		"src/cli.test.ts.cli > inner > adds": ev.Passed,
		"src/cli.test.ts.cli > fails":        ev.Failed,
		"src/cli.test.ts.cli > skipped":      ev.Skipped,
		"src/cli.test.ts.top":                ev.Passed,
	}
	if got := outcomes(j); !maps.Equal(got, want) {
		t.Fatalf("cases %v\nwant %v; the recorded vitest report has an import error, a failure, a skip and two passes", got, want)
	}
	if text := failingText(j, "src/broken.test.ts.(file)"); !strings.Contains(text, "Cannot find module './nope'") {
		t.Fatalf("import-error case text %q lacks vitest's message; the spec requires the tool's output so the developer sees which import broke", text)
	}
	if text := failingText(j, "src/cli.test.ts.cli > fails"); !strings.Contains(text, "expected 2 to be 3") {
		t.Fatalf("failed case text %q lacks the assertion message", text)
	}

	c := callOf(t, r.data, "vitest")
	if c.cwd != r.top {
		t.Fatalf("vitest ran in %s, want the unit %s; the unit's vitest config applies only from there", c.cwd, r.top)
	}
	for _, flag := range []string{"related", "--run", "--sequence.shuffle", "--passWithNoTests"} {
		if !slices.Contains(c.args, flag) {
			t.Fatalf("vitest args %q lack %s; the spec runs related tests once (not watch), shuffled, and treats no related test as zero cases", c.args, flag)
		}
	}
	if want := []string{filepath.Join(r.top, "src", "broken.test.ts"), filepath.Join(r.top, "src", "parse.ts")}; !slices.Equal(c.files(), want) {
		t.Fatalf("vitest file args %q, want the changed source and test files %q; vitest related finds their importers", c.files(), want)
	}
}

func TestRunRecordsEveryUnitsTools(t *testing.T) {
	versions := map[string]string{"packages/a": "5.0.1", "packages/b": "5.0.2", "packages/c": "5.0.1"}
	files := map[string]string{".gitignore": "node_modules\n"}
	tools := map[string]map[string]string{}
	for unit, v := range versions {
		files[unit+"/package.json"] = "{}"
		files[unit+"/package-lock.json"] = "{}"
		files[unit+"/src/x.ts"] = ""
		tools[unit] = map[string]string{"vitest": v}
	}
	isolateGit(t)
	fakeNodeOnPath(t)
	top, data := realTempDir(t), t.TempDir()
	buildRepo(t, top, data, files, tools)
	t.Chdir(top)
	for unit := range versions {
		writeTree(t, top, map[string]string{unit + "/src/x.ts": "changed"})
		replay(t, filepath.Join(data, unit), "vitest", replayed{report: `{"testResults":[]}`})
	}
	out := t.TempDir()
	resp := runIn(t, "VER-TESTS-PASS", out)
	if j := junitIn(t, out, resp); j.Cases != 0 {
		t.Fatalf("%d cases from empty reports", j.Cases)
	}
	if got := resp.ToolVersions["vitest"]; got != "5.0.1, 5.0.2" {
		t.Fatalf("tool_versions vitest = %q, want \"5.0.1, 5.0.2\"; every unit's version must be recorded, each once, so a run that mixed versions says so", got)
	}
	if got := resp.PinnedBy["vitest"]; got != "packages/a/package-lock.json, packages/b/package-lock.json, packages/c/package-lock.json" {
		t.Fatalf("pinned_by vitest = %q; every unit's lockfile must be recorded", got)
	}
	for unit := range versions {
		c := callOf(t, filepath.Join(data, unit), "vitest")
		dir := filepath.Join(top, filepath.FromSlash(unit))
		if !c.invoked || c.cwd != dir || !slices.Equal(c.files(), []string{filepath.Join(dir, "src", "x.ts")}) {
			t.Fatalf("unit %s: vitest call %+v; each unit runs its own vitest, in its directory, on its own files", unit, c)
		}
	}
}

func TestRunUntrackedLockfilesResolveMarkedOnce(t *testing.T) {
	units := []string{"packages/a", "packages/b"}
	files := map[string]string{".gitignore": "node_modules\n"}
	tools := map[string]map[string]string{}
	for _, unit := range units {
		files[unit+"/package.json"] = "{}"
		files[unit+"/package-lock.json"] = "{}"
		files[unit+"/src/x.ts"] = ""
		tools[unit] = map[string]string{"vitest": "5.0.4"}
	}
	isolateGit(t)
	fakeNodeOnPath(t)
	top, data := realTempDir(t), t.TempDir()
	buildRepo(t, top, data, files, tools)
	git(t, top, "rm", "-q", "--cached", "--", "packages/a/package-lock.json", "packages/b/package-lock.json")
	git(t, top, "commit", "-q", "-m", "keep lockfiles local")
	t.Chdir(top)
	for _, unit := range units {
		writeTree(t, top, map[string]string{unit + "/src/x.ts": "changed"})
		replay(t, filepath.Join(data, unit), "vitest", replayed{report: `{"testResults":[]}`})
	}
	out := t.TempDir()
	resp, raw := runInRaw(t, "VER-TESTS-PASS", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q; without authority: ci an untracked lockfile is reported, not refused", resp.Environment.Message)
	}
	if j := junitIn(t, out, resp); j.Cases != 0 {
		t.Fatalf("%d cases from empty reports", j.Cases)
	}
	if resp.ToolVersions["vitest"] != "5.0.4" || resp.PinnedBy["vitest"] != "packages/a/package-lock.json, packages/b/package-lock.json" {
		t.Fatalf("tool_versions %v pinned_by %v; the untracked lockfiles still pin vitest and are recorded", resp.ToolVersions, resp.PinnedBy)
	}
	if u, ok := runUntracked(t, raw); !ok || !slices.Equal(u, []string{"vitest"}) {
		t.Fatalf("untracked = %v (present %v), want [vitest] once; the evaluator fails each listed tool under authority: ci, and the protocol requires unique names even when several units pin the tool\n%s", u, ok, raw)
	}
}

func TestRunNoChangedTypeScriptFiles(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4", "eslint": "10.12.0", "typescript": "6.0.3"}})
	writeTree(t, r.top, map[string]string{"README.md": "changed\n", "notes.txt": "new\n"})
	for _, obj := range []string{"VER-TESTS-PASS", "CODE-ZERO-WARNINGS"} {
		out := t.TempDir()
		resp := runIn(t, obj, out)
		if resp.Environment != nil {
			t.Fatalf("%s: environment %q with no TypeScript change", obj, resp.Environment.Message)
		}
		if want := map[string]string{"node": fakeNodeVersion}; !maps.Equal(resp.ToolVersions, want) || len(resp.PinnedBy) != 0 {
			t.Fatalf("%s: tool_versions %v pinned_by %v; with no unit selected no tool is resolved", obj, resp.ToolVersions, resp.PinnedBy)
		}
		if obj == "VER-TESTS-PASS" {
			if j := junitIn(t, out, resp); j.Cases != 0 || len(j.Suites) != 0 {
				t.Fatalf("JUnit %+v; only non-TypeScript files changed, so the spec wants a JUnit file with zero cases, not an error", j)
			}
		} else if rs := sarifIn(t, out, resp); len(rs) != 0 {
			t.Fatalf("SARIF results %+v; only non-TypeScript files changed, so the spec wants zero results", rs)
		}
	}
	for bin := range slices.Values([]string{"vitest", "eslint", "tsc"}) {
		if callOf(t, r.data, bin).invoked {
			t.Fatalf("%s ran with no TypeScript change; nothing was selected to run it on", bin)
		}
	}
}

func TestRunUnitWithNoSelectedFileStillTypeChecks(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4", "eslint": "10.12.0", "typescript": "6.0.3"}})
	writeTree(t, r.top, map[string]string{"src/env.d.ts": "declare const x: number;\n"})
	replay(t, r.data, "tsc", replayed{stdout: recorded(t, "tsc-syntax.txt", r.unreal), code: 2})

	out := t.TempDir()
	if j := junitIn(t, out, runIn(t, "VER-TESTS-PASS", out)); j.Cases != 0 {
		t.Fatalf("%d cases; a changed declaration file selects no test file", j.Cases)
	}
	if callOf(t, r.data, "vitest").invoked {
		t.Fatal("vitest ran with no selected file; vitest related with no file argument would run every test")
	}

	out = t.TempDir()
	rs := sarifIn(t, out, runIn(t, "CODE-ZERO-WARNINGS", out))
	if callOf(t, r.data, "eslint").invoked {
		t.Fatal("eslint ran with no selected file; with no file argument it lints nothing useful or everything")
	}
	if !callOf(t, r.data, "tsc").invoked || len(rs) != 1 || rs[0].RuleID != "TS1134" || rs[0].URI != "src/syntax.ts" {
		t.Fatalf("results %+v; a changed declaration can break callers, so tsc still checks the unit and its diagnostics are reported", rs)
	}
}

func TestRunLintReportsTypeErrorElsewhere(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"eslint": "10.12.0", "typescript": "6.0.3"}})
	writeTree(t, r.top, map[string]string{"src/lint.ts": "export const a: any = 1; const unused = 2;\n"})
	replay(t, r.data, "eslint", replayed{stdout: recorded(t, "eslint.json", r.unreal), code: 1})
	replay(t, r.data, "tsc", replayed{stdout: recorded(t, "tsc.txt", r.unreal), code: 2})
	out := t.TempDir()
	resp := runIn(t, "CODE-ZERO-WARNINGS", out)
	if want := map[string]string{"node": fakeNodeVersion, "eslint": "10.12.0", "typescript": "6.0.3"}; !maps.Equal(resp.ToolVersions, want) {
		t.Fatalf("tool_versions %v, want %v; vitest is not installed and CODE-ZERO-WARNINGS must not need it", resp.ToolVersions, want)
	}
	got := sarifIn(t, out, resp)
	want := slices.Concat(eslintRecorded, tscRecorded)
	sameResults(t, "only src/lint.ts changed; every ESLint finding in it and every tsc diagnostic in the unit, including src/typeerr.ts which did not change, must be reported", got, want)

	e := callOf(t, r.data, "eslint")
	if i := slices.Index(e.args, "--format"); i < 0 || i+1 >= len(e.args) || e.args[i+1] != "json" || e.cwd != r.top {
		t.Fatalf("eslint call %+v; the converter reads --format json, run from the unit so the repository's config applies", e)
	}
	if !slices.Equal(e.files(), []string{filepath.Join(r.top, "src", "lint.ts")}) {
		t.Fatalf("eslint files %q, want only the selected src/lint.ts", e.files())
	}
	if c := callOf(t, r.data, "tsc"); !slices.Equal(c.args, []string{"--noEmit", "-p", filepath.Join(r.top, "tsconfig.json"), "--pretty", "false"}) || c.cwd != r.top {
		t.Fatalf("tsc call %+v; the parser reads --pretty false lines, and tsc must check the unit's own tsconfig.json without writing output", c)
	}
}

var (
	eslintRecorded = []ev.Result{
		{RuleID: "@typescript-eslint/no-explicit-any", Level: "error", Message: "Unexpected any. Specify a different type.", URI: "src/lint.ts"},
		{RuleID: "@typescript-eslint/no-unused-vars", Level: "error", Message: "'unused' is assigned a value but never used.", URI: "src/lint.ts"},
	}
	eslintFatalRecorded = []ev.Result{{RuleID: "parse", Level: "error", Message: "Parsing error: Variable declaration expected.", URI: "src/syntax.ts"}}
	tscRecorded         = []ev.Result{
		{RuleID: "TS2307", Level: "error", Message: "Cannot find module './nope' or its corresponding type declarations.", URI: "src/broken.test.ts"},
		{RuleID: "TS2322", Level: "error", Message: "Type 'string' is not assignable to type 'number'.", URI: "src/multi.ts"},
		{RuleID: "TS2322", Level: "error", Message: "Type 'number' is not assignable to type 'string'.", URI: "src/multi.ts"},
		{RuleID: "TS2322", Level: "error", Message: "Type 'string' is not assignable to type 'number'.", URI: "src/typeerr.ts"},
	}
	tscSyntaxRecorded = []ev.Result{{RuleID: "TS1134", Level: "error", Message: "Variable declaration expected.", URI: "src/syntax.ts"}}
	tscGlobalRecorded = []ev.Result{{RuleID: "TS5058", Level: "error", Message: "The specified path does not exist: 'nope.json'."}}
)

func TestRunLintFollowsToolExitCodes(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"eslint": "10.12.0", "typescript": "6.0.3"}})
	writeTree(t, r.top, map[string]string{"src/lint.ts": "changed"})
	eslintOuts := map[string][]ev.Result{"eslint.json": eslintRecorded, "eslint-fatal.json": eslintFatalRecorded, "": nil}
	tscOuts := map[string][]ev.Result{"tsc.txt": tscRecorded, "tsc-syntax.txt": tscSyntaxRecorded, "tsc-global.txt": tscGlobalRecorded, "": nil}
	rapid.Check(t, func(t *rapid.T) {
		eName := rapid.SampledFrom(slices.Sorted(maps.Keys(eslintOuts))).Draw(t, "eslintOutput")
		eCode := rapid.IntRange(0, 2).Draw(t, "eslintExit")
		tName := rapid.SampledFrom(slices.Sorted(maps.Keys(tscOuts))).Draw(t, "tscOutput")
		tCode := rapid.IntRange(0, 2).Draw(t, "tscExit")
		eOut, tOut := "Oops! Something went wrong!", "Segmentation fault"
		if eName != "" {
			eOut = recorded(t, eName, r.unreal)
		}
		if tName != "" {
			tOut = recorded(t, tName, r.unreal)
		}
		replay(t, r.data, "eslint", replayed{stdout: eOut, stderr: "eslint stderr", code: eCode})
		replay(t, r.data, "tsc", replayed{stdout: tOut, code: tCode})
		out, err := os.MkdirTemp(r.data, "out-*")
		if err != nil {
			t.Fatalf("%v", err)
		}
		got := sarifIn(t, out, runIn(t, "CODE-ZERO-WARNINGS", out))

		var want []ev.Result
		if eCode == 2 || eName == "" {
			want = append(want, ev.Result{RuleID: "eslint", Level: "error"})
		} else {
			want = append(want, eslintOuts[eName]...)
		}
		want = append(want, tscOuts[tName]...)
		if tName == "" && tCode != 0 {
			want = append(want, ev.Result{RuleID: "tsc", Level: "error"})
		}
		if len(got) != len(want) {
			t.Fatalf("eslint %q exit %d, tsc %q exit %d: got %+v\nwant %+v; ESLint exit 2 or unreadable output, and tsc failing with no diagnostic, must each be an error result rather than a clean pass", eName, eCode, tName, tCode, got, want)
		}
		for i := range want {
			if want[i].RuleID == "eslint" || want[i].RuleID == "tsc" {
				got[i].Message = ""
			}
			if got[i] != want[i] {
				t.Fatalf("result %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})
}

func TestRunLintNeedsTsconfigAndESLintConfig(t *testing.T) {
	isolateGit(t)
	fakeNodeOnPath(t)
	base := realTempDir(t)
	t.Chdir(base)
	rapid.Check(t, func(t *rapid.T) {
		top, err := os.MkdirTemp(base, "repo-*")
		if err != nil {
			t.Fatalf("%v", err)
		}
		data := filepath.Join(top, ".data")
		files := map[string]string{
			".gitignore":                     "node_modules\n.data\n",
			"packages/api/package.json":      "{}",
			"packages/api/package-lock.json": "{}",
			"packages/api/src/a.ts":          "",
		}
		hasTsconfig := rapid.Bool().Draw(t, "tsconfig")
		if hasTsconfig {
			files["packages/api/tsconfig.json"] = "{}"
		}
		where := rapid.SampledFrom([]string{"none", "packages/api/", "packages/", ""}).Draw(t, "eslintConfigDir")
		if where != "none" {
			files[where+"eslint.config."+rapid.SampledFrom(specConfigExts).Draw(t, "ext")] = ""
		}
		buildRepo(t, top, data, files, map[string]map[string]string{"packages/api": {"eslint": "10.12.0", "typescript": "6.0.3"}})
		writeTree(t, top, map[string]string{"packages/api/src/a.ts": "changed"})
		replay(t, filepath.Join(data, "packages", "api"), "eslint", replayed{stdout: "[]"})
		replay(t, filepath.Join(data, "packages", "api"), "tsc", replayed{})
		out := filepath.Join(top, ".data", "out")
		if err := os.Chdir(top); err != nil {
			t.Fatalf("%v", err)
		}
		resp := runIn(t, "CODE-ZERO-WARNINGS", out)

		switch {
		case !hasTsconfig:
			if resp.Environment == nil {
				t.Fatalf("no environment without packages/api/tsconfig.json; tsc --noEmit has no project to check, so a pass would be unchecked")
			}
			mustMention(t, resp.Environment.Message, top, "packages/api/tsconfig.json")
		case where == "none":
			if resp.Environment == nil {
				t.Fatalf("no environment without an eslint.config.*; ESLint would run with no rules and pass everything")
			}
			mustMention(t, resp.Environment.Message, top, "packages/api", "eslint.config")
		default:
			if resp.Environment != nil {
				t.Fatalf("environment %q with tsconfig.json present and eslint config in %q; ESLint finds flat config in the unit or any parent", resp.Environment.Message, where)
			}
			if rs := sarifIn(t, out, resp); len(rs) != 0 {
				t.Fatalf("results %+v from clean tool output", rs)
			}
			return
		}
		if len(resp.Evidence) != 0 || exists(filepath.Join(out, "lint.sarif")) {
			t.Fatalf("environment response with evidence %+v; an environment problem writes no evidence", resp.Evidence)
		}
		if callOf(t, filepath.Join(data, "packages", "api"), "eslint").invoked || callOf(t, filepath.Join(data, "packages", "api"), "tsc").invoked {
			t.Fatal("a lint tool ran although the unit's configuration was missing")
		}
	})
}

func TestRunEnvironmentWritesNoEvidence(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, obj string
		setup     func(t *testing.T, r tsRepo)
		words     []string
	}{
		{"node missing", "VER-TESTS-PASS", func(t *testing.T, _ tsRepo) {
			bin := t.TempDir()
			if err := os.Symlink(gitBin, filepath.Join(bin, "git")); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
		}, []string{"node", "PATH"}},
		{"node missing for lint", "CODE-ZERO-WARNINGS", func(t *testing.T, _ tsRepo) { t.Setenv("PATH", t.TempDir()) }, []string{"node", "PATH"}},
		{"vitest missing", "VER-TESTS-PASS", func(t *testing.T, r tsRepo) {
			if err := os.RemoveAll(filepath.Join(r.top, "node_modules", "vitest")); err != nil {
				t.Fatal(err)
			}
		}, []string{"vitest", "npm ci"}},
		{"typescript missing", "CODE-ZERO-WARNINGS", func(t *testing.T, r tsRepo) {
			if err := os.RemoveAll(filepath.Join(r.top, "node_modules", "typescript")); err != nil {
				t.Fatal(err)
			}
		}, []string{"typescript", "npm ci"}},
		{"plug'n'play", "VER-TESTS-PASS", func(t *testing.T, r tsRepo) { writeTree(t, r.top, map[string]string{".pnp.cjs": ""}) }, []string{".pnp.cjs", "unsupported"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4", "eslint": "10.12.0", "typescript": "6.0.3"}})
			writeTree(t, r.top, map[string]string{"src/parse.ts": "changed"})
			tc.setup(t, r)
			out := t.TempDir()
			resp := runIn(t, tc.obj, out)
			if resp.Environment == nil {
				t.Fatalf("%s: no environment; assure must be told the run could not happen rather than read missing evidence", tc.name)
			}
			mustMention(t, resp.Environment.Message, r.top, tc.words...)
			if entries, _ := os.ReadDir(out); len(resp.Evidence) != 0 || len(entries) != 0 {
				t.Fatalf("%s: evidence %+v, files %v; an environment response writes no evidence", tc.name, resp.Evidence, entries)
			}
			for _, bin := range []string{"vitest", "eslint", "tsc"} {
				if callOf(t, r.data, bin).invoked {
					t.Fatalf("%s: %s ran; the adapter must resolve and check before running any tool", tc.name, bin)
				}
			}
		})
	}
}

func TestRunUsageErrors(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
	out := t.TempDir()
	for _, args := range [][]string{
		{"run"},
		{"run", "VER-TESTS-PASS"},
		{"run", "VER-TESTS-PASS", "--out", out},
		{"run", "VER-TESTS-PASS", "--changed-from", "HEAD"},
		{"run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out, "extra"},
		{"run", "VER-TESTS-PASS", "--bogus"},
		{"run", "VER-NO-SUCH-OBJECTIVE", "--changed-from", "HEAD", "--out", out},
	} {
		var o, e bytes.Buffer
		if code := run(args, &o, &e); code != 2 || o.Len() != 0 || e.Len() == 0 {
			t.Fatalf("%q exited %d stdout %q stderr %q; a malformed or unimplemented run is a usage error (2) on stderr, never a response assure could read as a verdict", args, code, o.String(), e.String())
		}
	}
	code, stdout, stderr := runRaw("VER-TESTS-PASS", out)
	if code != 0 {
		t.Fatalf("baseline run exited %d: %s", code, stderr)
	}
	var o, e bytes.Buffer
	if code := run([]string{"run", "VER-TESTS-PASS", "--changed-from", "no-such-ref", "--out", out}, &o, &e); code != 1 || o.Len() != 0 || !strings.Contains(e.String(), "no-such-ref") {
		t.Fatalf("unknown ref: exit %d stdout %q stderr %q; a git failure must exit 1 naming the cause, not report an empty selection (baseline stdout %s)", code, o.String(), e.String(), stdout)
	}
	if callOf(t, r.data, "vitest").invoked {
		t.Fatal("vitest ran for an unchanged tree")
	}
}

func TestRunTestsSetsSeedOfHead(t *testing.T) {
	t.Setenv("ASSURE_SEED", "0")
	r := newRepo(t, nil, nil)
	failOnBaseFake(t, r.top, r.data, r.top, ".")
	writeTree(t, r.top, map[string]string{"src/parse.ts": "export const parse = 2;\n"})

	runIn(t, "VER-TESTS-PASS", t.TempDir())
	if !slices.Contains(callOf(t, r.data, "related.head").args, "related") {
		t.Fatal("vitest related did not run for a changed source")
	}
	seed, _ := fileText(t, filepath.Join(r.data, "related.head.seed"))
	if want := checkSeed(t, headOf(t, r.top)); seed != want {
		t.Fatalf("vitest related saw ASSURE_SEED %q, want %q from HEAD; VER-TESTS-PASS must draw the same property inputs on the same commit as coverage, fuzz, fail-on-base and mutation, so a property failure replays", seed, want)
	}
}

func TestRecordJoinsDistinctValues(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vals := rapid.SliceOfN(rapid.SampledFrom([]string{"5.0.1", "5.0.2", "5.0.10", "6.0.3"}), 1, 6).Draw(t, "values")
		m := map[string]string{"other": "x"}
		var distinct []string
		for _, v := range vals {
			record(m, "vitest", v)
			if !slices.Contains(distinct, v) {
				distinct = append(distinct, v)
			}
		}
		if want := strings.Join(distinct, ", "); m["vitest"] != want || m["other"] != "x" {
			t.Fatalf("record %q gave %q, want %q; each unit's version appears once, in first-seen order, and other keys are untouched", vals, m["vitest"], want)
		}
	})
}
