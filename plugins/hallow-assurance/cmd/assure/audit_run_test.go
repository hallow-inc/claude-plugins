package main

import (
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
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const (
	objTests    = "VER-TESTS-PASS"
	objMutation = "VER-MUTATION-CHANGED"
	objCoverage = "VER-COVERAGE-RESOLUTION"
	objFuzz     = "VER-ROBUST-FUZZ"
)

var (
	auditable     = []string{objTests, objMutation, objCoverage, objFuzz}
	listable      = append(slices.Clone(auditable), "VER-FAIL-ON-BASE", "FM-COMPLETE")
	auditLangs    = []string{"xx", "yy"}
	auditFiles    = []string{"p/a.xx", "p/a_test.xx", "q/b.xx", "p/c.yy", "q/d.yy", "q/d_test.yy"}
	auditFileLen  = 10
	greenJUnit    = `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`
	redJUnit      = `<testsuite name="p" tests="1" failures="1"><testcase classname="p" name="TestBroken"><failure message="boom"/></testcase></testsuite>`
	emptyRun      = `{"protocol":2,"evidence":[],"tool_versions":{"t":"1"},"pinned_by":{}}`
	testsRun      = `{"protocol":2,"evidence":[{"type":"test.junit","path":"ev"}],"tool_versions":{"t":"1"},"pinned_by":{}}`
	auditEnvRun   = `{"protocol":2,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + envMessage + `"}}`
	levelsGen     = rapid.SampledFrom([]string{"A", "B", "C", "D"})
	auditStateDir = filepath.Join(".assure", "state", "audit")
)

type auditPlanFixture struct {
	repo
	bin  string
	logs map[string]string
}

func writeOrFail(t fataler, p, body string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

func newAuditPlanFixture(t *testing.T) auditPlanFixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	r := newRepo(t, "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: B\ncomponents: []\n")
	r.write(".gitignore", ".assure/state/\n")
	for _, f := range auditFiles {
		r.write(f, numbered(auditFileLen))
	}
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("-c", "commit.gpgsign=false", "-c", "core.excludesFile="+os.DevNull, "commit", "-qm", "base")
	return fakeAdapterFixture(t, r, auditLangs...)
}

func fakeAdapterFixture(t *testing.T, r repo, langs ...string) auditPlanFixture {
	t.Helper()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	f := auditPlanFixture{repo: r, bin: bin, logs: map[string]string{}}
	for _, lang := range langs {
		f.logs[lang] = filepath.Join(bin, lang+".d")
	}
	return f
}

// install rewrites the adapter script for lang; describe is embedded so the script's hash, and
// with it the describe cache entry, changes whenever the listed objectives change.
func (f auditPlanFixture) install(t fataler, lang string, listed []string, responses map[string]string) {
	d := f.logs[lang]
	_ = os.RemoveAll(d)
	for name, body := range responses {
		writeOrFail(t, filepath.Join(d, name), body)
	}
	writeOrFail(t, filepath.Join(d, "default.json"), emptyRun)
	objs := make([]string, len(listed))
	for i, o := range listed {
		objs[i] = fmt.Sprintf(`"%s":{"tool":"t","fast":false}`, o)
	}
	ext := lang
	describe := `{"protocol":2,"languages":["` + lang + `"],"claims":["**/*.` + ext + `"],"patterns":{"test":["**/*_test.` + ext + `"]},"objectives":{` + strings.Join(objs, ",") + `}}`
	script := `#!/bin/sh
d='` + d + `'
case "$1" in
describe) printf '%s' '` + describe + `' ;;
classify)
  shift
  printf '{"protocol":0,"files":['
  sep=
  for p in "$@"; do
    case "$p" in
    gen/*.` + ext + `) printf '%s{"path":"%s","language":"` + lang + `","role":"generated"}' "$sep" "$p"; sep=, ;;
    *_test.` + ext + `) printf '%s{"path":"%s","language":"` + lang + `","role":"test"}' "$sep" "$p"; sep=, ;;
    *.` + ext + `) printf '%s{"path":"%s","language":"` + lang + `","role":"source"}' "$sep" "$p"; sep=, ;;
    esac
  done
  printf ']}' ;;
run)
  printf 'start %s\n' "$*" >> "$d/log"
  obj=$2
  shift 2
  scope=
  out=
  while [ $# -gt 0 ]; do
    case "$1" in
    --scope) scope=$2; shift ;;
    --out) out=$2; shift ;;
    esac
    shift
  done
  if [ -n "$scope" ]; then cp "$scope" "$d/scope.$obj.json"; fi
  mkdir -p "$out"
  if [ -f "$d/$obj.ev" ]; then cp "$d/$obj.ev" "$out/ev"; fi
  if [ -f "$d/$obj.fail" ]; then printf 'end %s\n' "$obj" >> "$d/log"; cat "$d/$obj.fail" >&2; exit 3; fi
  if [ -f "$d/$obj.json" ]; then cat "$d/$obj.json"; else cat "$d/default.json"; fi
  printf 'end %s\n' "$obj" >> "$d/log" ;;
*) echo "assure-adapter-` + lang + `: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(f.bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
}

type adapterRun struct {
	objective string
	argv      []string
}

func (f auditPlanFixture) runsOf(t fataler, lang string) (runs []adapterRun, starts []int, testsEnd int) {
	testsEnd = -1
	data, err := os.ReadFile(filepath.Join(f.logs[lang], "log"))
	if os.IsNotExist(err) {
		return nil, nil, -1
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		kind, rest, _ := strings.Cut(line, " ")
		switch kind {
		case "start":
			argv := strings.Fields(rest)
			if len(argv) < 2 {
				t.Fatalf("%s adapter logged %q", lang, line)
			}
			runs = append(runs, adapterRun{objective: argv[1], argv: argv})
			starts = append(starts, i)
		case "end":
			if rest == objTests {
				testsEnd = i
			}
		}
	}
	return runs, starts, testsEnd
}

func flagValue(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

// auditOutcomes parses the interim 5.2 stdout. Task 5.3 replaces stdout with findings.json; only
// this helper should need to change then.
func auditOutcomes(t fataler, out string) map[string]string {
	got := map[string]string{}
	var keys []string
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			t.Fatalf("stdout line %q: want <lang>\\t<objective>\\t<status>[\\t<reason>]", line)
		}
		key := f[0] + "\t" + f[1]
		if _, dup := got[key]; dup {
			t.Fatalf("stdout reports %q twice:\n%s", key, out)
		}
		got[key] = f[2]
		keys = append(keys, key)
	}
	if !slices.IsSorted(keys) {
		t.Fatalf("stdout not sorted by language then objective:\n%s", out)
	}
	return got
}

func applicableAt(m *core.Manifest, id string, files []app.ScopeFile) bool {
	for _, o := range m.Catalog.Objectives {
		if o.ID != id {
			continue
		}
		for _, f := range files {
			cf := m.ChangedFile(f.Path)
			if s := o.Levels[cf.Level]; (s == "required" || s == "advisory") && (o.AppliesTo != "inputs" || cf.Inputs) {
				return true
			}
		}
	}
	return false
}

func drawAuditManifest(t *rapid.T) string {
	comp := func(dir string) string {
		c := "  - {path: '" + dir + "/**', level: " + levelsGen.Draw(t, dir+" level")
		if rapid.Bool().Draw(t, dir+" inputs") {
			c += ", inputs: true"
		}
		return c + "}\n"
	}
	return "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: " + levelsGen.Draw(t, "default level") + "\ncomponents:\n" + comp("p") + comp("q")
}

func drawAuditScope(t *rapid.T) []string {
	if rapid.IntRange(0, 4).Draw(t, "whole repo") == 0 {
		return []string{"."}
	}
	files := rapid.SliceOfNDistinct(rapid.SampledFrom(auditFiles), 1, len(auditFiles), rapid.ID[string]).Draw(t, "files")
	args := make([]string, len(files))
	for i, p := range files {
		args[i] = p
		if rapid.Bool().Draw(t, "ranged "+p) {
			start := rapid.IntRange(1, auditFileLen).Draw(t, "start "+p)
			args[i] = fmt.Sprintf("%s:%d-%d", p, start, rapid.IntRange(start, auditFileLen).Draw(t, "end "+p))
		}
	}
	return args
}

func scopeEntriesOf(files []app.ScopeFile) adapterkit.Scope {
	var s adapterkit.Scope
	for _, f := range files {
		s = append(s, adapterkit.ScopeEntry{Path: f.Path, Ranges: f.Ranges})
	}
	slices.SortFunc(s, func(a, b adapterkit.ScopeEntry) int { return strings.Compare(a.Path, b.Path) })
	return s
}

func (f auditPlanFixture) checkScopeFile(t *rapid.T, lang string, r adapterRun, want adapterkit.Scope) {
	argv := r.argv
	scope, ok := flagValue(argv, "--scope")
	if !ok || slices.Contains(argv, "--changed-from") {
		t.Fatalf("%s %s argv %q: every audit run must pass --scope and never --changed-from; an audit has no base commit", lang, r.objective, argv)
	}
	if !filepath.IsAbs(scope) {
		scope = filepath.Join(f.root, scope)
	}
	if dir := filepath.Join(f.root, auditStateDir, lang) + string(filepath.Separator); !strings.HasPrefix(filepath.Clean(scope), dir) {
		t.Fatalf("%s %s: scope file %s is not under %s; one scope file per language keeps one language's files out of another adapter's run", lang, r.objective, scope, dir)
	}
	got, err := adapterkit.ReadScope(filepath.Join(f.logs[lang], "scope."+r.objective+".json"))
	if err != nil {
		t.Fatalf("%s %s: the scope file audit wrote does not read back through the adapter's own reader: %v", lang, r.objective, err)
	}
	slices.SortFunc(got, func(a, b adapterkit.ScopeEntry) int { return strings.Compare(a.Path, b.Path) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s %s: scope file %#v, want %#v; the adapter must see exactly this language's resolved scope, ranges included, or it audits code the user did not name", lang, r.objective, got, want)
	}
}

func TestAuditEnvironmentResponseMarksLanguageNotAuditedAndEverythingElseStillRuns(t *testing.T) {
	f := newAuditPlanFixture(t)
	writeOrFail(t, filepath.Join(f.root, "assurance.yaml"), "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: A\ncomponents:\n  - {path: '**', level: A, inputs: true}\n")
	rapid.Check(t, func(t *rapid.T) {
		envLang := rapid.SampledFrom(auditLangs).Draw(t, "environment language")
		envObj := rapid.SampledFrom(auditable).Draw(t, "environment objective")
		listed := map[string][]string{}
		for _, lang := range auditLangs {
			listed[lang] = rapid.SliceOfDistinct(rapid.SampledFrom(listable), rapid.ID[string]).Draw(t, lang+" listed")
			resp := map[string]string{objTests + ".json": testsRun, objTests + ".ev": greenJUnit}
			if lang == envLang {
				if !slices.Contains(listed[lang], envObj) {
					listed[lang] = append(listed[lang], envObj)
				}
				if envObj == objMutation && !slices.Contains(listed[lang], objTests) {
					listed[lang] = append(listed[lang], objTests)
				}
				resp[envObj+".json"] = auditEnvRun
				delete(resp, envObj+".ev")
			}
			f.install(t, lang, listed[lang], resp)
		}

		_, out, stderr := assure("audit", ".")

		want := map[string]string{}
		for _, lang := range auditLangs {
			invoked := envExpectation(lang, listed[lang], envLang, envObj, want)
			runs, _, _ := f.runsOf(t, lang)
			var ran []string
			for _, r := range runs {
				ran = append(ran, r.objective)
			}
			if !slices.Equal(slices.Sorted(slices.Values(ran)), slices.Sorted(slices.Values(invoked))) {
				t.Fatalf("%s adapter ran %q, want %q (environment on %s %s); one objective's missing tool must not stop the rest of that language, nor another language, from being audited, but mutation runs only after a confirmed green suite because an unknown suite may score every mutant killed\nstdout:\n%s\nstderr:\n%s", lang, ran, invoked, envLang, envObj, out, stderr)
			}
		}
		got := auditOutcomes(t, out)
		if s := got[envLang+"\t"+envObj]; s == "ran" {
			t.Fatalf("%s %s reported ran though the adapter answered environment; the user would read a missing tool as a clean result\nstdout:\n%s", envLang, envObj, out)
		}
		delete(got, envLang+"\t"+envObj)
		notAudited := got[envLang+"\t-"]
		if !strings.HasPrefix(notAudited, "not-audited\t") || !strings.Contains(notAudited, envMessage) {
			t.Fatalf("%s outcome %q, want not-audited naming the adapter's message %q; a missing tool hides weaknesses, so it must not look like a clean audit\nstdout:\n%s", envLang, notAudited, envMessage, out)
		}
		delete(got, envLang+"\t-")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("outcomes %q, want %q besides the not-audited line (environment on %s %s)\nstdout:\n%s", got, want, envLang, envObj, out)
		}
	})
}

func plannedAudit(m *core.Manifest, listed []string, files []app.ScopeFile) []string {
	var planned []string
	for _, id := range auditable {
		if len(files) > 0 && slices.Contains(listed, id) && applicableAt(m, id, files) {
			planned = append(planned, id)
		}
	}
	return planned
}

func (f auditPlanFixture) checkLanguageRuns(t *rapid.T, lang string, invoked []string, files []app.ScopeFile, context string) {
	runs, starts, testsEnd := f.runsOf(t, lang)
	var ran []string
	for _, r := range runs {
		ran = append(ran, r.objective)
	}
	if slices.Contains(ran, "VER-FAIL-ON-BASE") {
		t.Fatalf("%s adapter was invoked with VER-FAIL-ON-BASE (%q); an audit has no base commit to fail on", lang, ran)
	}
	if !slices.Equal(slices.Sorted(slices.Values(ran)), slices.Sorted(slices.Values(invoked))) {
		t.Fatalf("%s: adapter ran %q, want %q (%s); audit runs exactly the listed audit objectives that apply at the scope's levels, and never mutation against a red suite since every mutant would count as killed", lang, ran, invoked, context)
	}
	for i, r := range runs {
		if r.objective != objTests && slices.Contains(invoked, objTests) && (testsEnd < 0 || starts[i] < testsEnd) {
			t.Fatalf("%s: %s started before VER-TESTS-PASS finished (log lines %d, %d); the suite's colour decides whether mutation may run, so tests must finish first", lang, r.objective, starts[i], testsEnd)
		}
		f.checkScopeFile(t, lang, r, scopeEntriesOf(files))
	}
}

func TestAuditRunsOnlyApplicableListedAuditObjectivesTestsFirst(t *testing.T) {
	f := newAuditPlanFixture(t)
	rapid.Check(t, func(t *rapid.T) {
		writeOrFail(t, filepath.Join(f.root, "assurance.yaml"), drawAuditManifest(t))
		listed, red := map[string][]string{}, map[string]bool{}
		for _, lang := range auditLangs {
			listed[lang] = rapid.SliceOfDistinct(rapid.SampledFrom(listable), rapid.ID[string]).Draw(t, lang+" listed")
			red[lang] = slices.Contains(listed[lang], objTests) && rapid.Bool().Draw(t, lang+" red")
			junit := greenJUnit
			if red[lang] {
				junit = redJUnit
			}
			f.install(t, lang, listed[lang], map[string]string{objTests + ".json": testsRun, objTests + ".ev": junit})
		}
		args := drawAuditScope(t)
		m, err := core.LoadManifest(filepath.Join(f.root, "assurance.yaml"))
		if err != nil {
			t.Fatalf("%v", err)
		}
		scope, err := app.ResolveScope(m, f.root, args)
		if err != nil {
			t.Fatalf("resolving %q: %v", args, err)
		}

		code, out, stderr := assure(append([]string{"audit"}, args...)...)

		if code != 0 {
			t.Fatalf("assure audit %q: exit %d stdout %q stderr %q; every language ran, and a red suite is a finding, not a failure to audit", args, code, out, stderr)
		}
		want := map[string]string{}
		for _, lang := range auditLangs {
			files := slices.DeleteFunc(slices.Clone(scope), func(sf app.ScopeFile) bool { return sf.Lang != lang })
			planned := plannedAudit(m, listed[lang], files)
			suiteState := map[bool]string{true: "tests-failing", false: "tests-unknown"}[red[lang]]
			stopMutation := red[lang] || !slices.Contains(planned, objTests)
			var invoked []string
			for _, id := range planned {
				if id == objMutation && stopMutation {
					want[lang+"\t"+id] = "skipped\t" + suiteState
					continue
				}
				invoked = append(invoked, id)
				want[lang+"\t"+id] = "ran"
			}
			f.checkLanguageRuns(t, lang, invoked, files, fmt.Sprintf("listed %q, red %v, scope args %q, resolved %+v", listed[lang], red[lang], args, scope))
		}
		if got := auditOutcomes(t, out); !reflect.DeepEqual(got, want) {
			t.Fatalf("assure audit %q: outcomes %q, want %q\nstdout:\n%s", args, got, want, out)
		}
	})
}

func envExpectation(lang string, listed []string, envLang, envObj string, want map[string]string) (invoked []string) {
	suiteUnknown := (lang == envLang && envObj == objTests) || !slices.Contains(listed, objTests)
	for _, id := range auditable {
		switch {
		case !slices.Contains(listed, id):
		case id == objMutation && suiteUnknown:
			want[lang+"\t"+id] = "skipped\ttests-unknown"
		default:
			invoked = append(invoked, id)
			if lang != envLang || id != envObj {
				want[lang+"\t"+id] = "ran"
			}
		}
	}
	return invoked
}

type unconfirmedSuite struct {
	responses map[string]string
	errorText []string
}

var unconfirmedSuites = map[string]unconfirmedSuite{
	"non-zero exit":      {map[string]string{objTests + ".fail": "suite-runner-crashed"}, []string{"suite-runner-crashed"}},
	"protocol violation": {map[string]string{objTests + ".json": `{"protocol":2,"evidence":[]}`}, []string{"violates the protocol"}},
	"no junit evidence":  {map[string]string{objTests + ".json": emptyRun}, nil},
	"unreadable junit":   {map[string]string{objTests + ".json": testsRun, objTests + ".ev": "<testsuite"}, nil},
}

func TestAuditMutationWaitsForAConfirmedGreenSuite(t *testing.T) {
	f := newAuditPlanFixture(t)
	writeOrFail(t, filepath.Join(f.root, "assurance.yaml"), "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: A\ncomponents:\n  - {path: '**', level: A, inputs: true}\n")
	rapid.Check(t, func(t *rapid.T) {
		bad := rapid.SampledFrom(auditLangs).Draw(t, "unconfirmed language")
		kind := rapid.SampledFrom(slices.Sorted(maps.Keys(unconfirmedSuites))).Draw(t, "kind")
		suite := unconfirmedSuites[kind]
		for _, lang := range auditLangs {
			resp := map[string]string{objTests + ".json": testsRun, objTests + ".ev": greenJUnit}
			if lang == bad {
				resp = suite.responses
			}
			f.install(t, lang, auditable, resp)
		}

		_, out, stderr := assure("audit", ".")

		want := map[string]string{}
		for _, lang := range auditLangs {
			invoked := auditable
			if lang == bad {
				invoked = []string{objTests, objCoverage, objFuzz}
				want[lang+"\t"+objMutation] = "skipped\ttests-unknown"
			}
			for _, id := range invoked {
				if lang != bad || id != objTests {
					want[lang+"\t"+id] = "ran"
				}
			}
			runs, _, _ := f.runsOf(t, lang)
			var ran []string
			for _, r := range runs {
				ran = append(ran, r.objective)
			}
			if !slices.Equal(slices.Sorted(slices.Values(ran)), slices.Sorted(slices.Values(slices.Clone(invoked)))) {
				t.Fatalf("%s adapter ran %q, want %q (%s on %s VER-TESTS-PASS); mutation needs a confirmed green suite, since against an unknown suite every mutant may count as killed, but coverage and fuzz do not depend on it\nstdout:\n%s\nstderr:\n%s", lang, ran, invoked, kind, bad, out, stderr)
			}
		}
		got := auditOutcomes(t, out)
		if suite.errorText != nil {
			checkAdapterErrorNotAudited(t, got, bad, kind, suite.errorText, out)
		}
		delete(got, bad+"\t"+objTests)
		delete(got, bad+"\t-")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("outcomes %q, want %q (%s on %s VER-TESTS-PASS)\nstdout:\n%s", got, want, kind, bad, out)
		}
	})
}

func checkAdapterErrorNotAudited(t *rapid.T, got map[string]string, lang, kind string, errorText []string, out string) {
	if got[lang+"\t"+objTests] == "ran" {
		t.Fatalf("%s VER-TESTS-PASS reported ran though the adapter failed (%s)\nstdout:\n%s", lang, kind, out)
	}
	notAudited := got[lang+"\t-"]
	for _, part := range append([]string{"assure-adapter-" + lang}, errorText...) {
		if !strings.HasPrefix(notAudited, "not-audited\t") || !strings.Contains(notAudited, part) {
			t.Fatalf("%s outcome %q, want not-audited containing %q (%s); a failed adapter hides weaknesses, so the user must see why\nstdout:\n%s", lang, notAudited, part, kind, out)
		}
	}
}

func TestAuditThatCannotWriteItsStateExitsOneNamingTheCause(t *testing.T) {
	f := newAuditPlanFixture(t)
	for _, lang := range auditLangs {
		f.install(t, lang, auditable, map[string]string{objTests + ".json": testsRun, objTests + ".ev": greenJUnit})
	}
	state := filepath.Dir(auditStateDir)
	f.write(state, "a file where the state directory belongs\n")

	code, out, stderr := assure("audit", "p/a.xx", "q/d.yy")

	if code != 1 || out != "" || !strings.Contains(stderr, state) {
		t.Fatalf("exit %d stdout %q stderr %q; an audit that cannot write its scope files is an environment failure (exit 1) naming the path, never a clean empty audit", code, out, stderr)
	}
	for _, lang := range auditLangs {
		if runs, _, _ := f.runsOf(t, lang); len(runs) > 0 {
			t.Fatalf("%s adapter ran %+v though no scope file could be written", lang, runs)
		}
	}
}
