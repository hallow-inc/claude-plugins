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

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const fakeNodeVersion = "v26.8.1"

var recordedDir, _ = filepath.Abs(filepath.Join("testdata", "recorded"))

var fakeBins = map[string]string{"vitest": "vitest"}

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
		t.Fatalf("environment %q; Stryker and fast-check are not installed but VER-TESTS-PASS uses only vitest, and an unused tool must not block it", resp.Environment.Message)
	}
	if u, ok := runUntracked(t, raw); ok {
		t.Fatalf("untracked %v with a tracked lockfile; absent means every pin is tracked, and a spurious mark reports a tracked pin as untracked", u)
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
		t.Fatalf("environment %q; an untracked lockfile is reported, not refused", resp.Environment.Message)
	}
	if j := junitIn(t, out, resp); j.Cases != 0 {
		t.Fatalf("%d cases from empty reports", j.Cases)
	}
	if resp.ToolVersions["vitest"] != "5.0.4" || resp.PinnedBy["vitest"] != "packages/a/package-lock.json, packages/b/package-lock.json" {
		t.Fatalf("tool_versions %v pinned_by %v; the untracked lockfiles still pin vitest and are recorded", resp.ToolVersions, resp.PinnedBy)
	}
	if u, ok := runUntracked(t, raw); !ok || !slices.Equal(u, []string{"vitest"}) {
		t.Fatalf("untracked = %v (present %v), want [vitest] once; the report lists each listed tool, and the protocol requires unique names even when several units pin the tool\n%s", u, ok, raw)
	}
}

func TestRunNoChangedTypeScriptFiles(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
	writeTree(t, r.top, map[string]string{"README.md": "changed\n", "notes.txt": "new\n"})
	out := t.TempDir()
	resp := runIn(t, "VER-TESTS-PASS", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q with no TypeScript change", resp.Environment.Message)
	}
	if want := map[string]string{"node": fakeNodeVersion}; !maps.Equal(resp.ToolVersions, want) || len(resp.PinnedBy) != 0 {
		t.Fatalf("tool_versions %v pinned_by %v; with no unit selected no tool is resolved", resp.ToolVersions, resp.PinnedBy)
	}
	if j := junitIn(t, out, resp); j.Cases != 0 || len(j.Suites) != 0 {
		t.Fatalf("JUnit %+v; only non-TypeScript files changed, so the spec wants a JUnit file with zero cases, not an error", j)
	}
	if callOf(t, r.data, "vitest").invoked {
		t.Fatal("vitest ran with no TypeScript change; nothing was selected to run it on")
	}
}

func TestRunUnitWithNoSelectedFileRunsNoTests(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
	writeTree(t, r.top, map[string]string{"src/env.d.ts": "declare const x: number;\n"})
	out := t.TempDir()
	if j := junitIn(t, out, runIn(t, "VER-TESTS-PASS", out)); j.Cases != 0 {
		t.Fatalf("%d cases; a changed declaration file selects no test file", j.Cases)
	}
	if callOf(t, r.data, "vitest").invoked {
		t.Fatal("vitest ran with no selected file; vitest related with no file argument would run every test")
	}
}

func TestRunTestsNeedsNeitherTypeScriptVersionNorESLintConfig(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
	installTool(t, r.top, "typescript", "7.0.2")
	if adapterkit.Exists(filepath.Join(r.top, "eslint.config.js")) {
		t.Fatal("fixture has an eslint config; the scenario needs a unit without one")
	}
	writeTree(t, r.top, map[string]string{"src/parse.ts": "export const parse = 2;\n"})
	replay(t, r.data, "vitest", replayed{report: `{"testResults":[]}`})
	out := t.TempDir()
	resp := runIn(t, "VER-TESTS-PASS", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q; typescript 7.0.2 is outside the range the retired lint path parsed, and no eslint config exists, but VER-TESTS-PASS uses neither", resp.Environment.Message)
	}
	if _, ok := resp.ToolVersions["typescript"]; ok {
		t.Fatalf("tool_versions %v records typescript; only the tools the objective ran belong there", resp.ToolVersions)
	}
	junitIn(t, out, resp)
	if !callOf(t, r.data, "vitest").invoked {
		t.Fatal("vitest did not run for a changed source")
	}
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
		{"vitest missing", "VER-TESTS-PASS", func(t *testing.T, r tsRepo) {
			if err := os.RemoveAll(filepath.Join(r.top, "node_modules", "vitest")); err != nil {
				t.Fatal(err)
			}
		}, []string{"vitest", "npm ci"}},
		{"plug'n'play", "VER-TESTS-PASS", func(t *testing.T, r tsRepo) { writeTree(t, r.top, map[string]string{".pnp.cjs": ""}) }, []string{".pnp.cjs", "unsupported"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, nil, map[string]map[string]string{".": {"vitest": "5.0.4"}})
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
			for _, bin := range []string{"vitest"} {
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
