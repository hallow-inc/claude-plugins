package app

import (
	"cmp"
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
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const auditAdapterScript = `#!/bin/sh
d='DIR'
case "$1" in
describe) printf '%s' '{"protocol":2,"languages":["LANG"],"claims":["**/*.LANG"],"patterns":{"test":["**/*_test.LANG"]},"objectives":{OBJS}}' ;;
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
  if [ -f "$d/$obj.json" ]; then cat "$d/$obj.json"; else printf '%s' '{"protocol":2,"evidence":[],"tool_versions":{"t":"1"},"pinned_by":{}}'; fi
  printf 'end %s\n' "$obj" >> "$d/log" ;;
*) echo "assure-adapter-LANG: unexpected $*" >&2; exit 1 ;;
esac
`

const (
	auditTestsRun   = `{"protocol":2,"evidence":[{"type":"test.junit","path":"ev"}],"tool_versions":{"t":"1"},"pinned_by":{}}`
	auditEmptyRun   = `{"protocol":2,"evidence":[],"tool_versions":{"t":"1"},"pinned_by":{}}`
	auditGreenJUnit = `<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>`
	auditRedJUnit   = `<testsuite name="p" tests="1" failures="1"><testcase classname="p" name="TestBroken"><failure message="boom"/></testcase></testsuite>`
)

var (
	auditTestLangs = []string{"xx", "yy"}
	auditPool      = []string{"p/a.xx", "q/b_test.xx", "p/c.yy", "q/d.yy"}
	auditListable  = append(slices.Clone(auditObjectives), "VER-FAIL-ON-BASE", "FM-COMPLETE")
	auditLevels    = rapid.SampledFrom([]string{"A", "B", "C", "D"})
	suiteKinds     = []string{"green", "red", "no-junit", "unreadable-junit", "environment", "error"}
	otherKinds     = []string{"ok", "environment", "error"}
)

type auditFixture struct {
	root string
	bin  string
}

func newAuditFixture(t *testing.T) auditFixture {
	t.Helper()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return auditFixture{root: root, bin: bin}
}

func (f auditFixture) logDir(lang string) string { return filepath.Join(f.bin, lang+".d") }

func (f auditFixture) uninstall(t fataler, lang string) {
	t.Helper()
	for _, p := range []string{f.logDir(lang), filepath.Join(f.bin, "assure-adapter-"+lang)} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatalf("%v", err)
		}
	}
}

func (f auditFixture) install(t fataler, lang string, listed []string, responses map[string]string) {
	t.Helper()
	f.uninstall(t, lang)
	for name, body := range responses {
		putFile(t, f.logDir(lang), name, body)
	}
	if err := os.MkdirAll(f.logDir(lang), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	objs := make([]string, len(listed))
	for i, o := range listed {
		objs[i] = fmt.Sprintf(`"%s":{"tool":"t","fast":false}`, o)
	}
	script := strings.NewReplacer("DIR", f.logDir(lang), "LANG", lang, "OBJS", strings.Join(objs, ",")).Replace(auditAdapterScript)
	if err := os.WriteFile(filepath.Join(f.bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
}

type auditAdapterRun struct {
	objective string
	argv      []string
	start     int
}

func (f auditFixture) runs(t fataler, lang string) (runs []auditAdapterRun, testsEnd int) {
	t.Helper()
	testsEnd = -1
	data, err := os.ReadFile(filepath.Join(f.logDir(lang), "log"))
	if os.IsNotExist(err) {
		return nil, -1
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
			runs = append(runs, auditAdapterRun{objective: argv[1], argv: argv, start: i})
		case "end":
			if rest == auditTests {
				testsEnd = i
			}
		}
	}
	return runs, testsEnd
}

func responsesFor(lang, id, kind string) map[string]string {
	switch kind {
	case "green":
		return map[string]string{id + ".json": auditTestsRun, id + ".ev": auditGreenJUnit}
	case "red":
		return map[string]string{id + ".json": auditTestsRun, id + ".ev": auditRedJUnit}
	case "no-junit":
		return map[string]string{id + ".json": auditEmptyRun}
	case "unreadable-junit":
		return map[string]string{id + ".json": auditTestsRun, id + ".ev": "<testsuite"}
	case "environment":
		return map[string]string{id + ".json": `{"protocol":2,"evidence":[],"tool_versions":{},"pinned_by":{},"environment":{"message":"` + kindMessage(lang, id, kind) + `"}}`}
	case "error":
		return map[string]string{id + ".fail": kindMessage(lang, id, kind)}
	}
	return nil
}

func kindMessage(lang, id, kind string) string { return kind + "-" + lang + "-" + id }

type auditLangDraw struct {
	installed bool
	listed    []string
	kinds     map[string]string
}

type auditDraw struct {
	levels  map[string]string
	inputs  map[string]bool
	dropped []string
	langs   map[string]auditLangDraw
	scope   []ScopeFile
}

func drawAudit(t *rapid.T) auditDraw {
	d := auditDraw{levels: map[string]string{}, inputs: map[string]bool{}, langs: map[string]auditLangDraw{}}
	for _, dir := range []string{"p", "q"} {
		d.levels[dir] = auditLevels.Draw(t, dir+" level")
		d.inputs[dir] = rapid.Bool().Draw(t, dir+" inputs")
	}
	d.dropped = rapid.SliceOfDistinct(rapid.SampledFrom(auditObjectives), rapid.ID[string]).Filter(func(s []string) bool { return len(s) <= 1 }).Draw(t, "dropped from catalog")
	for _, lang := range auditTestLangs {
		ld := auditLangDraw{
			installed: rapid.IntRange(0, 5).Draw(t, lang+" installed") > 0,
			listed:    rapid.SliceOfDistinct(rapid.SampledFrom(auditListable), rapid.ID[string]).Draw(t, lang+" listed"),
			kinds:     map[string]string{},
		}
		for _, id := range auditObjectives {
			kinds := otherKinds
			if id == auditTests {
				kinds = suiteKinds
			}
			ld.kinds[id] = rapid.SampledFrom(kinds).Draw(t, lang+" "+id)
		}
		d.langs[lang] = ld
	}
	for _, p := range rapid.SliceOfDistinct(rapid.SampledFrom(auditPool), rapid.ID[string]).Draw(t, "scope paths") {
		sf := ScopeFile{Path: p, Lang: strings.TrimPrefix(filepath.Ext(p), "."), Role: "source"}
		if rapid.Bool().Draw(t, "ranged "+p) {
			start := rapid.IntRange(1, 20).Draw(t, "start "+p)
			sf.Ranges = [][2]int{{start, rapid.IntRange(start, 20).Draw(t, "end "+p)}}
		}
		d.scope = append(d.scope, sf)
	}
	return d
}

func (d auditDraw) manifest() string {
	comp := func(dir string) string {
		c := fmt.Sprintf("  - {path: '%s/**', level: %s", dir, d.levels[dir])
		if d.inputs[dir] {
			c += ", inputs: true"
		}
		return c + "}\n"
	}
	return "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: B\ncomponents:\n" + comp("p") + comp("q")
}

func (d auditDraw) applies(t *rapid.T, o core.Objective, files []ScopeFile) bool {
	for _, f := range files {
		dir, _, _ := strings.Cut(f.Path, "/")
		switch o.AppliesTo {
		case "":
		case "inputs":
			if !d.inputs[dir] {
				continue
			}
		default:
			t.Fatalf("%s applies to %q; this model knows only all files and inputs", o.ID, o.AppliesTo)
		}
		if s := o.Levels[core.Level(d.levels[dir])]; s == "required" || s == "advisory" {
			return true
		}
	}
	return false
}

type expectedLang struct {
	invoked  []string
	outcomes map[string]AuditOutcome
	reasons  []string
}

func (d auditDraw) expect(t *rapid.T, m *core.Manifest, lang string, files []ScopeFile) expectedLang {
	ld := d.langs[lang]
	e := expectedLang{outcomes: map[string]AuditOutcome{}}
	if !ld.installed {
		e.reasons = []string{"assure-adapter-" + lang + " lookup: not found on PATH"}
		return e
	}
	var planned []string
	for _, id := range auditObjectives {
		if !slices.Contains(ld.listed, id) {
			continue
		}
		if slices.Contains(d.dropped, id) {
			e.reasons = append(e.reasons, "assure-adapter-"+lang+" lists "+id+", but catalog v0 has no such objective")
			continue
		}
		i := slices.IndexFunc(m.Catalog.Objectives, func(o core.Objective) bool { return o.ID == id })
		if d.applies(t, m.Catalog.Objectives[i], files) {
			planned = append(planned, id)
		}
	}
	ran := func(id string) {
		e.invoked = append(e.invoked, id)
		switch k := ld.kinds[id]; k {
		case "environment", "error":
			e.reasons = append(e.reasons, kindMessage(lang, id, k))
		default:
			e.outcomes[id] = AuditOutcome{Lang: lang, Objective: id, Status: auditRan}
		}
	}
	suite := skipTestsUnknown
	if slices.Contains(planned, auditTests) {
		ran(auditTests)
		suite = map[string]string{"green": "", "red": skipTestsFailing}[ld.kinds[auditTests]]
		if k := ld.kinds[auditTests]; k != "green" && k != "red" {
			suite = skipTestsUnknown
		}
	}
	for _, id := range planned {
		switch {
		case id == auditTests:
		case id == auditMutation && suite != "":
			e.outcomes[id] = AuditOutcome{Lang: lang, Objective: id, Status: auditSkipped, Reason: suite}
		default:
			ran(id)
		}
	}
	return e
}

func (f auditFixture) checkRuns(t *rapid.T, lang string, e expectedLang, files []ScopeFile, context string) {
	runs, testsEnd := f.runs(t, lang)
	var ran []string
	for _, r := range runs {
		ran = append(ran, r.objective)
	}
	if !slices.Equal(slices.Sorted(slices.Values(ran)), slices.Sorted(slices.Values(e.invoked))) {
		t.Fatalf("%s adapter ran %q, want %q (%s); audit runs exactly the listed audit objectives the catalog knows and that apply at the scope's levels, and runs mutation only after a confirmed green suite, since against a red or unknown suite every mutant may count as killed", lang, ran, e.invoked, context)
	}
	want := make(adapterkit.Scope, len(files))
	for i, sf := range files {
		want[i] = adapterkit.ScopeEntry{Path: sf.Path, Ranges: sf.Ranges}
	}
	slices.SortFunc(want, func(a, b adapterkit.ScopeEntry) int { return strings.Compare(a.Path, b.Path) })
	for _, r := range runs {
		if r.objective != auditTests && slices.Contains(e.invoked, auditTests) && (testsEnd < 0 || r.start < testsEnd) {
			t.Fatalf("%s: %s started before VER-TESTS-PASS finished; the suite's colour decides whether mutation may run, so tests finish first (%s)", lang, r.objective, context)
		}
		f.checkRunScope(t, lang, r, want)
	}
}

func (f auditFixture) checkRunScope(t *rapid.T, lang string, r auditAdapterRun, want adapterkit.Scope) {
	langDir := filepath.Join(f.root, AuditDir, lang)
	scope, okScope := flagValueOf(r.argv, "--scope")
	out, okOut := flagValueOf(r.argv, "--out")
	if !okScope || !okOut || slices.Contains(r.argv, "--changed-from") {
		t.Fatalf("%s %s argv %q: an audit run passes --scope and --out and never --changed-from; an audit has no base commit", lang, r.objective, r.argv)
	}
	if filepath.Dir(scope) != langDir || filepath.Dir(out) != langDir {
		t.Fatalf("%s %s: scope %s and out %s, want both under %s; one directory per language keeps one language's files out of another adapter's run", lang, r.objective, scope, out, langDir)
	}
	got, err := adapterkit.ReadScope(filepath.Join(f.logDir(lang), "scope."+r.objective+".json"))
	if err != nil {
		t.Fatalf("%s %s: scope file does not read back through the adapter's reader: %v", lang, r.objective, err)
	}
	slices.SortFunc(got, func(a, b adapterkit.ScopeEntry) int { return strings.Compare(a.Path, b.Path) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s %s: scope file %#v, want %#v; the adapter must see exactly this language's scope, ranges included", lang, r.objective, got, want)
	}
}

func checkNotAudited(t *rapid.T, lang string, o AuditOutcome, reasons []string, context string) {
	if o.Status != auditNotAudited || o.Objective != "-" {
		t.Fatalf("%s outcome %+v, want one not-audited line for the language (%s)", lang, o, context)
	}
	for _, part := range reasons {
		if n := strings.Count(o.Reason, part); n != 1 {
			t.Fatalf("%s not-audited reason %q names %q %d times, want once (%s); a missing tool or failed adapter hides weaknesses, so the user must see each cause", lang, o.Reason, part, n, context)
		}
	}
}

func flagValueOf(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

func (f auditFixture) seedStaleAuditDir(t *rapid.T) {
	dir := filepath.Join(f.root, AuditDir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("%v", err)
	}
	if rapid.Bool().Draw(t, "audit dir is a stale file") {
		putFile(t, f.root, AuditDir, "stale\n")
		return
	}
	putFile(t, f.root, filepath.Join(AuditDir, "stale", "scope.json"), "[]")
}

func (f auditFixture) installDraw(t *rapid.T, d auditDraw) {
	for _, lang := range auditTestLangs {
		ld := d.langs[lang]
		if !ld.installed {
			f.uninstall(t, lang)
			continue
		}
		resp := map[string]string{}
		for id, kind := range ld.kinds {
			maps.Copy(resp, responsesFor(lang, id, kind))
		}
		f.install(t, lang, ld.listed, resp)
	}
}

func (f auditFixture) checkLanguage(t *rapid.T, d auditDraw, m *core.Manifest, lang string, got []AuditOutcome, context string) (inScope bool) {
	files := slices.DeleteFunc(slices.Clone(d.scope), func(sf ScopeFile) bool { return sf.Lang != lang })
	byObj := map[string]AuditOutcome{}
	for _, o := range got {
		if o.Lang != lang {
			continue
		}
		if _, dup := byObj[o.Objective]; dup {
			t.Fatalf("%s %s reported twice: %+v", lang, o.Objective, got)
		}
		byObj[o.Objective] = o
	}
	if len(files) == 0 {
		if runs, _ := f.runs(t, lang); len(byObj) > 0 || len(runs) > 0 {
			t.Fatalf("%s has no file in scope but reported %+v and its adapter ran %+v", lang, byObj, runs)
		}
		return false
	}
	e := d.expect(t, m, lang, files)
	if len(e.reasons) > 0 {
		checkNotAudited(t, lang, byObj["-"], e.reasons, context)
		delete(byObj, "-")
	}
	if !reflect.DeepEqual(byObj, e.outcomes) {
		t.Fatalf("%s outcomes %+v, want %+v besides any not-audited line (%s)", lang, byObj, e.outcomes, context)
	}
	f.checkRuns(t, lang, e, files, context)
	return true
}

func (f auditFixture) checkAuditDir(t *rapid.T, langsInScope []string) {
	entries, err := os.ReadDir(filepath.Join(f.root, AuditDir))
	if err != nil && (!os.IsNotExist(err) || len(langsInScope) > 0) {
		t.Fatalf("reading the audit dir: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		dirs = append(dirs, e.Name())
	}
	if !slices.Equal(dirs, langsInScope) {
		t.Fatalf("audit dir holds %q, want exactly the languages in scope %q; a previous audit's leftovers must not pass for this audit's scope", dirs, langsInScope)
	}
	for _, lang := range langsInScope {
		if _, err := adapterkit.ReadScope(filepath.Join(f.root, AuditDir, lang, "scope.json")); err != nil {
			t.Fatalf("%s scope file: %v; each language in scope gets its scope file even when its adapter cannot run", lang, err)
		}
	}
}

func TestAuditRunsListedApplicableObjectivesTestsFirstAndReportsEveryLanguage(t *testing.T) {
	f := newAuditFixture(t)
	rapid.Check(t, func(t *rapid.T) {
		d := drawAudit(t)
		putFile(t, f.root, "assurance.yaml", d.manifest())
		m, err := core.LoadManifest(filepath.Join(f.root, "assurance.yaml"))
		if err != nil {
			t.Fatalf("%v", err)
		}
		full := slices.Clone(m.Catalog.Objectives)
		m.Catalog.Objectives = slices.DeleteFunc(slices.Clone(full), func(o core.Objective) bool { return slices.Contains(d.dropped, o.ID) })
		f.installDraw(t, d)
		f.seedStaleAuditDir(t)

		got, err := Audit(m, slices.Clone(d.scope))

		context := fmt.Sprintf("draw %+v", d)
		if err != nil {
			t.Fatalf("Audit: %v (%s); adapter failures are outcomes, not errors", err, context)
		}
		if !slices.IsSortedFunc(got, func(a, b AuditOutcome) int {
			return cmp.Or(cmp.Compare(a.Lang, b.Lang), cmp.Compare(a.Objective, b.Objective))
		}) {
			t.Fatalf("outcomes %+v not sorted by language then objective", got)
		}
		m.Catalog.Objectives = full
		var langsInScope []string
		for _, lang := range auditTestLangs {
			if f.checkLanguage(t, d, m, lang, got, context) {
				langsInScope = append(langsInScope, lang)
			}
		}
		f.checkAuditDir(t, langsInScope)
	})
}

var unwritableAuditStates = map[string]func(t fataler, root string){
	"state dir is a file": func(t fataler, root string) {
		putFile(t, root, filepath.Dir(AuditDir), "not a directory\n")
	},
	"state dir is a dangling symlink": func(t fataler, root string) {
		if err := os.MkdirAll(filepath.Join(root, ".assure"), 0o755); err != nil {
			t.Fatalf("%v", err)
		}
		if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, filepath.Dir(AuditDir))); err != nil {
			t.Fatalf("%v", err)
		}
	},
}

func TestAuditThatCannotWriteItsScopeFilesFailsBeforeRunningAnyAdapter(t *testing.T) {
	f := newAuditFixture(t)
	for _, lang := range auditTestLangs {
		f.install(t, lang, auditObjectives, responsesFor(lang, auditTests, "green"))
	}
	putFile(t, f.root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: A\ncomponents: []\n")
	m, err := core.LoadManifest(filepath.Join(f.root, "assurance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.SampledFrom(slices.Sorted(func(yield func(string) bool) {
			for k := range unwritableAuditStates {
				if !yield(k) {
					return
				}
			}
		})).Draw(t, "unwritable state")
		paths := rapid.SliceOfNDistinct(rapid.SampledFrom(auditPool), 1, len(auditPool), rapid.ID[string]).Draw(t, "scope paths")
		var scope []ScopeFile
		for _, p := range paths {
			scope = append(scope, ScopeFile{Path: p, Lang: strings.TrimPrefix(filepath.Ext(p), "."), Role: "source"})
		}
		if err := os.RemoveAll(filepath.Join(f.root, ".assure")); err != nil {
			t.Fatalf("%v", err)
		}
		unwritableAuditStates[kind](t, f.root)
		for _, lang := range auditTestLangs {
			if err := os.RemoveAll(filepath.Join(f.logDir(lang), "log")); err != nil {
				t.Fatalf("%v", err)
			}
		}

		got, err := Audit(m, scope)

		if err == nil || got != nil {
			t.Fatalf("Audit with %s: outcomes %+v, error %v; an audit that cannot hand adapters their scope must fail rather than report a partial audit", kind, got, err)
		}
		for _, lang := range auditTestLangs {
			if runs, _ := f.runs(t, lang); len(runs) > 0 {
				t.Fatalf("%s: %s adapter ran %+v though no scope file could be written", kind, lang, runs)
			}
		}
	})
}

func TestAuditFailsWhenAScopeFilePathIsADirectory(t *testing.T) {
	f := newAuditFixture(t)
	putFile(t, f.root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: A\ncomponents: []\n")
	m, err := core.LoadManifest(filepath.Join(f.root, "assurance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	scope := []ScopeFile{
		{Path: "p/a.xx", Lang: "w/../xx/scope.json", Role: "source"},
		{Path: "p/a.xx", Lang: "xx", Role: "source"},
	}

	got, err := Audit(m, scope)

	if err == nil || got != nil {
		t.Fatalf("outcomes %+v, error %v; the second language's scope.json is a directory the first created, and an unwritten scope file must fail the audit", got, err)
	}
}
