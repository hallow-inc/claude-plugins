package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	auditCoverage    = "VER-COVERAGE-RESOLUTION"
	auditFuzz        = "VER-ROBUST-FUZZ"
	kindMutant       = "surviving-mutant"
	kindUncovered    = "uncovered"
	kindMissingFuzz  = "missing-fuzz"
	kindTestsFailing = "tests-failing"
	unrelatedLine    = "// unrelated comment"
)

var (
	findingTexts    = []string{"x = x + 1", "return x", "}"}
	findingIndents  = []string{"", "\t", "    "}
	findingMutators = []string{"ARITHMETIC_BASE", "CONDITIONALS_NEGATION", "INCREMENT_DECREMENT"}
	mutantStatuses  = []string{"Killed", "Timeout", "Survived", "NoCoverage", "CompileError", "RuntimeError", "Ignored", "Pending"}
	findingFuncs    = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima"}
	fuzzCaseNames   = []string{"FuzzKiwi", "FuzzMango"}
	fuzzOutcomeTags = map[string]string{"passed": "", "failed": `<failure message="m"/>`, "errored": `<error message="m"/>`, "skipped": "<skipped/>"}
	redTestNames    = []string{"TestRedOne", "TestRedTwo", "TestRedThree"}
	findingIDRe     = regexp.MustCompile(`^([0-9a-f]{16})(?:-([1-9][0-9]*))?$`)
	findingsPool    = []struct {
		path, lang string
		source     bool
	}{
		{"p/a.xx", "xx", true}, {"q/e.xx", "xx", true}, {"q/b_test.xx", "xx", false}, {"p/c.yy", "yy", true}, {"q/d.yy", "yy", true},
	}
)

func auditGit(t fataler, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{
		"-c", "commit.gpgsign=false", "-c", "core.excludesFile=" + os.DevNull, "-c", "user.email=t@t", "-c", "user.name=t",
	}, args...)...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func commitFixture(t fataler, root string) string {
	t.Helper()
	auditGit(t, root, "add", "-A", "--", ".", ":(exclude).assure/state")
	auditGit(t, root, "commit", "-q", "--allow-empty", "-m", "fixture")
	return auditGit(t, root, "rev-parse", "HEAD")
}

func findingsPath(root string) string { return filepath.Join(root, AuditDir, "findings.json") }

func readFindings(t fataler, root, where string) AuditFindings {
	t.Helper()
	data, err := os.ReadFile(findingsPath(root))
	if err != nil {
		t.Fatalf("reading findings.json after a successful audit: %v (%s); the findings file is the audit's only durable output", err, where)
	}
	vs, err := schemas.Validate(schemas.AuditFindings, data)
	if err != nil || len(vs) > 0 {
		t.Fatalf("findings.json violates %s: %v %v (%s)\n%s", schemas.AuditFindings, vs, err, where, data)
	}
	var f AuditFindings
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("findings.json does not decode into AuditFindings: %v (%s)\n%s", err, where, data)
	}
	return f
}

func checkFindingsHeader(t fataler, root string, got AuditFindings, args []string, where string) {
	t.Helper()
	if head := auditGit(t, root, "rev-parse", "HEAD"); got.Commit != head {
		t.Fatalf("commit %q, want HEAD %q (%s); a finding is only reproducible at the commit it was found on", got.Commit, head, where)
	}
	dirty := auditGit(t, root, "status", "--porcelain", "--", ".", ":(exclude).assure/state") != ""
	if got.Dirty != dirty {
		t.Fatalf("dirty %v, want %v (%s); uncommitted edits mean the commit alone does not reproduce the findings, and audit's own state under .assure/state/ is not an edit", got.Dirty, dirty, where)
	}
	if !slices.Equal(got.Scope, args) {
		t.Fatalf("scope %q, want the audit arguments verbatim %q (%s)", got.Scope, args, where)
	}
	if !slices.IsSortedFunc(got.Findings, func(a, b AuditFinding) int {
		return cmp.Or(cmp.Compare(a.Language, b.Language), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	}) {
		t.Fatalf("findings not sorted by language, path, line, kind, id (%s): %+v", where, got.Findings)
	}
	if !slices.IsSortedFunc(got.Scores, func(a, b AuditScore) int {
		return cmp.Or(cmp.Compare(a.Language, b.Language), cmp.Compare(a.Level, b.Level))
	}) {
		t.Fatalf("scores not sorted by language, level (%s): %+v", where, got.Scores)
	}
	for i := 1; i < len(got.Unaudited); i++ {
		if got.Unaudited[i-1].Language >= got.Unaudited[i].Language {
			t.Fatalf("unaudited not sorted by language with one entry each (%s): %+v", where, got.Unaudited)
		}
	}
}

type modelMutant struct {
	line    int
	mutator string
	status  string
}

type modelFile struct {
	path, lang string
	source     bool
	lines      []string
	inScope    bool
	ranges     [][2]int
	mutants    []modelMutant
	hasRecord  bool
	funcs      []core.CoverFunc
	hits       map[int]int
	resolved   map[string]bool
	fuzz       string
	cases      []core.FuzzCase
}

func (f *modelFile) dir() string { d, _, _ := strings.Cut(f.path, "/"); return d }

func (f *modelFile) inRange(line int) bool {
	if line < 1 || line > len(f.lines) {
		return false
	}
	if f.ranges == nil {
		return true
	}
	return slices.ContainsFunc(f.ranges, func(r [2]int) bool { return r[0] <= line && line <= r[1] })
}

func (f *modelFile) funcAt(line int) string {
	for _, fn := range f.funcs {
		if fn.Start <= line && line <= fn.End {
			return fn.Name
		}
	}
	return ""
}

func (f *modelFile) text(line int) string { return strings.TrimSpace(f.lines[line-1]) }

type findingsDraw struct {
	levels   map[string]string
	inputs   map[string]bool
	dropTh   map[core.Level]bool
	files    []*modelFile
	red      map[string][]string
	versions map[string]map[string]string
}

func drawModelFile(t *rapid.T, path, lang string, source bool) *modelFile {
	f := &modelFile{path: path, lang: lang, source: source, hits: map[int]int{}, resolved: map[string]bool{}}
	for i := range rapid.IntRange(3, 7).Draw(t, path+" lines") {
		f.lines = append(f.lines, rapid.SampledFrom(findingIndents).Draw(t, fmt.Sprintf("%s indent %d", path, i+1))+rapid.SampledFrom(findingTexts).Draw(t, fmt.Sprintf("%s text %d", path, i+1)))
	}
	n := len(f.lines)
	f.inScope = rapid.IntRange(0, 2).Draw(t, path+" in scope") > 0
	if f.inScope && rapid.Bool().Draw(t, path+" ranged") {
		start := rapid.IntRange(1, n).Draw(t, path+" start")
		f.ranges = [][2]int{{start, rapid.IntRange(start, n).Draw(t, path+" end")}}
	}
	if !source {
		return f
	}
	if f.inScope {
		f.drawMutants(t)
	}
	f.drawCoverage(t)
	f.fuzz = rapid.SampledFrom([]string{"none", "empty", "cases"}).Draw(t, path+" fuzz")
	if f.fuzz == "cases" {
		for _, name := range rapid.SliceOfNDistinct(rapid.SampledFrom(fuzzCaseNames), 1, 2, rapid.ID[string]).Draw(t, path+" fuzz cases") {
			f.cases = append(f.cases, core.FuzzCase{Name: name, Outcome: rapid.SampledFrom([]string{"passed", "failed", "errored", "skipped"}).Draw(t, path+" "+name)})
		}
	}
	return f
}

func (f *modelFile) drawMutants(t *rapid.T) {
	var lines []int
	for l := 1; l <= len(f.lines); l++ {
		if f.inRange(l) {
			lines = append(lines, l)
		}
	}
	for i := range rapid.IntRange(0, 5).Draw(t, f.path+" mutants") {
		f.mutants = append(f.mutants, modelMutant{
			line:    rapid.SampledFrom(lines).Draw(t, fmt.Sprintf("%s mutant %d line", f.path, i)),
			mutator: rapid.SampledFrom(findingMutators).Draw(t, fmt.Sprintf("%s mutant %d mutator", f.path, i)),
			status:  rapid.SampledFrom(mutantStatuses).Draw(t, fmt.Sprintf("%s mutant %d status", f.path, i)),
		})
	}
}

func (f *modelFile) drawCoverage(t *rapid.T) {
	n := len(f.lines)
	f.hasRecord = rapid.IntRange(0, 4).Draw(t, f.path+" coverage record") > 0
	first := rapid.IntRange(1, min(3, n)).Draw(t, f.path+" first function")
	f.funcs = []core.CoverFunc{{Name: findingFuncs[0], Start: first}}
	for l := first + 1; l <= n; l++ {
		if rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("%s new function at %d", f.path, l)) == 0 {
			f.funcs[len(f.funcs)-1].End = l - 1
			f.funcs = append(f.funcs, core.CoverFunc{Name: findingFuncs[len(f.funcs)], Start: l})
		}
	}
	f.funcs[len(f.funcs)-1].End = n
	for l := 1; l <= n; l++ {
		switch rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("%s hits %d", f.path, l)) {
		case 1:
			f.hits[l] = 0
		case 2:
			f.hits[l] = rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("%s hit count %d", f.path, l))
		}
	}
	for _, fn := range f.funcs {
		f.resolved[fn.Name] = rapid.IntRange(0, 3).Draw(t, f.path+" resolved "+fn.Name) == 0
	}
}

func drawFindings(t *rapid.T) *findingsDraw {
	d := &findingsDraw{levels: map[string]string{}, inputs: map[string]bool{}, dropTh: map[core.Level]bool{}, red: map[string][]string{}, versions: map[string]map[string]string{}}
	for _, dir := range []string{"p", "q"} {
		d.levels[dir] = auditLevels.Draw(t, dir+" level")
		d.inputs[dir] = rapid.Bool().Draw(t, dir+" inputs")
	}
	for _, l := range []core.Level{"A", "B", "C"} {
		d.dropTh[l] = rapid.IntRange(0, 3).Draw(t, "drop threshold "+string(l)) == 0
	}
	for _, p := range findingsPool {
		d.files = append(d.files, drawModelFile(t, p.path, p.lang, p.source))
	}
	if !slices.ContainsFunc(d.files, func(f *modelFile) bool { return f.inScope }) {
		d.files[rapid.IntRange(0, len(d.files)-1).Draw(t, "forced scope file")].inScope = true
	}
	for _, lang := range auditTestLangs {
		if rapid.Bool().Draw(t, lang+" red") {
			d.red[lang] = rapid.SliceOfNDistinct(rapid.SampledFrom(redTestNames), 1, len(redTestNames), rapid.ID[string]).Draw(t, lang+" failing tests")
		}
		d.versions[lang] = map[string]string{}
		for _, id := range auditObjectives {
			d.versions[lang][id] = strconv.Itoa(rapid.IntRange(1, 9).Draw(t, lang+" "+id+" version"))
		}
	}
	return d
}

func (d *findingsDraw) level(f *modelFile) core.Level { return core.Level(d.levels[f.dir()]) }

func (d *findingsDraw) applies(o core.Objective, f *modelFile) bool {
	if o.AppliesTo == "inputs" && !d.inputs[f.dir()] {
		return false
	}
	s := o.Levels[d.level(f)]
	return s == "required" || s == "advisory"
}

func (d *findingsDraw) scopeOf(lang string) []*modelFile {
	var out []*modelFile
	for _, f := range d.files {
		if f.lang == lang && f.inScope {
			out = append(out, f)
		}
	}
	return out
}

func (d *findingsDraw) scopeArgs() ([]string, []ScopeFile) {
	var args []string
	var scope []ScopeFile
	for _, f := range d.files {
		if !f.inScope {
			continue
		}
		role := "test"
		if f.source {
			role = "source"
		}
		scope = append(scope, ScopeFile{Path: f.path, Lang: f.lang, Role: role, Ranges: slices.Clone(f.ranges)})
		arg := f.path
		for _, r := range f.ranges {
			arg += fmt.Sprintf(":%d-%d", r[0], r[1])
		}
		args = append(args, arg)
	}
	return args, scope
}

func (d *findingsDraw) manifest() string {
	s := "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: B\ncomponents:\n"
	for _, dir := range []string{"p", "q"} {
		inputs := ""
		if d.inputs[dir] {
			inputs = ", inputs: true"
		}
		s += fmt.Sprintf("  - {path: '%s/**', level: %s%s}\n", dir, d.levels[dir], inputs)
	}
	return s
}

func (d *findingsDraw) resolutions() string {
	var b strings.Builder
	for _, f := range d.files {
		for _, fn := range f.funcs {
			if f.resolved[fn.Name] {
				fmt.Fprintf(&b, "- {path: %s, function: %s, resolution: dead, rationale: unreachable since the loader checks first, approver: t}\n", f.path, fn.Name)
			}
		}
	}
	if b.Len() == 0 {
		return "[]\n"
	}
	return b.String()
}

func (d *findingsDraw) writeTree(t fataler, root string) {
	putFile(t, root, "assurance.yaml", d.manifest())
	putFile(t, root, core.ResolutionsFile, d.resolutions())
	for _, f := range d.files {
		putFile(t, root, f.path, strings.Join(f.lines, "\n")+"\n")
	}
}

func runResponse(typ string, versions map[string]string) string {
	data, _ := json.Marshal(map[string]any{"protocol": 2, "evidence": []map[string]string{{"type": typ, "path": "ev"}}, "tool_versions": versions, "pinned_by": map[string]string{}})
	return string(data)
}

func (d *findingsDraw) responses(lang string) map[string]string {
	v := d.versions[lang]
	resp := map[string]string{}
	var junit strings.Builder
	junit.WriteString(`<testsuite name="p"><testcase classname="p" name="TestFine"/>`)
	for _, n := range d.red[lang] {
		fmt.Fprintf(&junit, `<testcase classname="p" name="%s"><failure message="boom"/></testcase>`, n)
	}
	junit.WriteString("</testsuite>")
	resp[auditTests+".json"] = runResponse("test.junit", map[string]string{"tests": v[auditTests]})
	resp[auditTests+".ev"] = junit.String()

	report := map[string]any{}
	var lcov []evidence.LCOVFile
	var fuzz strings.Builder
	fuzz.WriteString("<testsuites>")
	for _, f := range d.files {
		if f.lang != lang || !f.source {
			continue
		}
		if len(f.mutants) > 0 {
			var ms []map[string]any
			for i, m := range f.mutants {
				pos := map[string]int{"line": m.line, "column": 1}
				ms = append(ms, map[string]any{"id": fmt.Sprintf("%s:%d:%d", f.path, m.line, i), "mutatorName": m.mutator, "status": m.status, "location": map[string]any{"start": pos, "end": pos}})
			}
			report[f.path] = map[string]any{"language": lang, "source": strings.Join(f.lines, "\n"), "mutants": ms}
		}
		if f.hasRecord {
			lf := evidence.LCOVFile{Path: f.path, Lines: f.hits}
			for _, fn := range f.funcs {
				lf.Functions = append(lf.Functions, evidence.LCOVFunction{Name: fn.Name, Start: fn.Start, End: fn.End})
			}
			lcov = append(lcov, lf)
		}
		if f.fuzz != "none" {
			fmt.Fprintf(&fuzz, `<testsuite name="%s"><properties><property name="assure.source" value="%s"/></properties>`, f.path, f.path)
			for _, c := range f.cases {
				fmt.Fprintf(&fuzz, `<testcase name="%s">%s</testcase>`, c.Name, fuzzOutcomeTags[c.Outcome])
			}
			fuzz.WriteString("</testsuite>")
		}
	}
	fuzz.WriteString("</testsuites>")
	mut, _ := json.Marshal(map[string]any{"schemaVersion": "1", "thresholds": map[string]int{"high": 80, "low": 60}, "files": report})
	resp[auditMutation+".json"] = runResponse("mutation.report", map[string]string{"mutation": v[auditMutation]})
	resp[auditMutation+".ev"] = string(mut)
	resp[auditCoverage+".json"] = runResponse("coverage.resolution", map[string]string{"coverage": v[auditCoverage]})
	resp[auditCoverage+".ev"] = string(evidence.FormatLCOV(lcov))
	resp[auditFuzz+".json"] = runResponse("fuzz.run", map[string]string{"fuzz": v[auditFuzz]})
	resp[auditFuzz+".ev"] = fuzz.String()
	return resp
}

type wantFinding struct {
	kind, lang, path string
	line             int
	level            core.Level
	markers          []string
	content          string
}

type wantAudit struct {
	findings     []wantFinding
	scores       map[string]AuditScore
	unaudited    map[string][]string
	toolVersions map[string]map[string]string
}

func objectiveOf(m *core.Manifest, id string) core.Objective {
	i := slices.IndexFunc(m.Catalog.Objectives, func(o core.Objective) bool { return o.ID == id })
	return m.Catalog.Objectives[i]
}

func (d *findingsDraw) expect(m *core.Manifest) wantAudit {
	w := wantAudit{scores: map[string]AuditScore{}, unaudited: map[string][]string{}, toolVersions: map[string]map[string]string{}}
	mutation, coverage, fuzz := objectiveOf(m, auditMutation), objectiveOf(m, auditCoverage), objectiveOf(m, auditFuzz)
	for _, lang := range auditTestLangs {
		files := d.scopeOf(lang)
		if len(files) == 0 {
			continue
		}
		runs := func(o core.Objective) bool {
			return slices.ContainsFunc(files, func(f *modelFile) bool { return d.applies(o, f) })
		}
		tv := map[string]string{"tests": d.versions[lang][auditTests]}
		if red := d.red[lang]; red != nil {
			w.findings = append(w.findings, wantFinding{kind: kindTestsFailing, lang: lang, markers: red, content: lang})
		} else if runs(mutation) {
			tv["mutation"] = d.versions[lang][auditMutation]
			d.expectMutants(mutation, lang, files, &w)
		}
		if runs(coverage) {
			tv["coverage"] = d.versions[lang][auditCoverage]
			d.expectCoverage(coverage, lang, files, &w)
		}
		if runs(fuzz) {
			tv["fuzz"] = d.versions[lang][auditFuzz]
			d.expectFuzz(fuzz, lang, files, &w)
		}
		w.toolVersions[lang] = tv
	}
	return w
}

func (d *findingsDraw) expectMutants(o core.Objective, lang string, files []*modelFile, w *wantAudit) {
	for _, f := range files {
		if !d.applies(o, f) {
			continue
		}
		for _, mu := range f.mutants {
			level := d.level(f)
			key := lang + "/" + string(level)
			s, ok := w.scores[key]
			if !ok {
				th, has := o.Threshold[level]
				if !has {
					th = 100
				}
				s = AuditScore{Language: lang, Level: level, Threshold: th, Advisory: true}
			}
			switch mu.status {
			case "Killed", "Timeout":
				s.Detected++
				s.Total++
			case "Survived", "NoCoverage":
				s.Total++
				w.findings = append(w.findings, wantFinding{kind: kindMutant, lang: lang, path: f.path, line: mu.line, level: level, markers: []string{mu.mutator}, content: f.text(mu.line) + "\x00" + mu.mutator})
			default:
				continue
			}
			w.scores[key] = s
		}
	}
	for k, s := range w.scores {
		s.Percent = 100 * float64(s.Detected) / float64(s.Total)
		w.scores[k] = s
	}
}

func (d *findingsDraw) expectCoverage(o core.Objective, lang string, files []*modelFile, w *wantAudit) {
	for _, f := range files {
		if !f.source || !d.applies(o, f) {
			continue
		}
		if !f.hasRecord {
			w.unaudited[lang] = append(w.unaudited[lang], f.path)
			continue
		}
		for l := 1; l <= len(f.lines); l++ {
			hits, ok := f.hits[l]
			if !ok || hits > 0 || !f.inRange(l) {
				continue
			}
			fn := f.funcAt(l)
			marker := fn
			if fn == "" {
				marker = "outside any function"
			} else if f.resolved[fn] {
				continue
			}
			w.findings = append(w.findings, wantFinding{kind: kindUncovered, lang: lang, path: f.path, line: l, level: d.level(f), markers: []string{marker}, content: fn + "\x00" + f.text(l)})
		}
	}
}

func (d *findingsDraw) expectFuzz(o core.Objective, lang string, files []*modelFile, w *wantAudit) {
	for _, f := range files {
		if !f.source || !d.applies(o, f) {
			continue
		}
		add := func(marker string) {
			w.findings = append(w.findings, wantFinding{kind: kindMissingFuzz, lang: lang, path: f.path, level: d.level(f), markers: []string{f.path, marker}})
		}
		switch f.fuzz {
		case "none":
			add("no fuzz evidence names this file")
		case "empty":
			add("no fuzz target covers this file")
		}
		for _, c := range f.cases {
			if c.Outcome != "passed" {
				add("fuzz target " + c.Name + " " + c.Outcome)
			}
		}
	}
}

func matches(w wantFinding, g AuditFinding) bool {
	if w.kind != g.Kind || w.lang != g.Language || w.path != g.Path || w.line != g.Line || w.level != g.Level {
		return false
	}
	for _, m := range w.markers {
		if !strings.Contains(g.Detail, m) {
			return false
		}
	}
	return true
}

func checkFindingsMatchModel(t *rapid.T, got AuditFindings, w wantAudit, where string) {
	pending := slices.Clone(w.findings)
	groups := map[string][]AuditFinding{}
	for _, g := range got.Findings {
		i := slices.IndexFunc(pending, func(x wantFinding) bool { return matches(x, g) })
		if i < 0 {
			t.Fatalf("unexpected finding %+v (%s)\nall findings %+v\nwant %+v", g, where, got.Findings, w.findings)
		}
		content := pending[i].content
		if g.Kind == kindMissingFuzz {
			content = g.Detail
		}
		key := strings.Join([]string{g.Language, g.Path, g.Kind, content}, "\x01")
		groups[key] = append(groups[key], g)
		pending = slices.Delete(pending, i, i+1)
	}
	if len(pending) > 0 {
		t.Fatalf("missing findings %+v (%s)\ngot %+v", pending, where, got.Findings)
	}
	checkFindingIDs(t, groups, where)
	checkScores(t, got.Scores, w.scores, where)
	checkUnaudited(t, got.Unaudited, w.unaudited, where)
	if !reflect.DeepEqual(got.ToolVersions, w.toolVersions) {
		t.Fatalf("tool_versions %v, want %v (%s); each language's versions are the merge of the adapter runs that audit made, so a finding can be traced to the tool that produced it", got.ToolVersions, w.toolVersions, where)
	}
}

func checkScores(t *rapid.T, got []AuditScore, want map[string]AuditScore, where string) {
	scores := map[string]AuditScore{}
	for _, s := range got {
		key := s.Language + "/" + string(s.Level)
		if _, dup := scores[key]; dup {
			t.Fatalf("score for %s reported twice (%s): %+v", key, where, got)
		}
		if w, ok := want[key]; ok && math.Abs(s.Percent-w.Percent) < 1e-9 {
			s.Percent = w.Percent
		}
		scores[key] = s
	}
	if !reflect.DeepEqual(scores, want) {
		t.Fatalf("scores %+v, want %+v (%s); each level's score sits next to the catalog threshold for that level (100 when the catalog sets none), and a red suite has no score since every mutant would count as killed", scores, want, where)
	}
}

func checkUnaudited(t *rapid.T, got []AuditUnaudited, want map[string][]string, where string) {
	unaudited := map[string]string{}
	for _, u := range got {
		unaudited[u.Language] = u.Message
	}
	if !slices.Equal(slices.Sorted(maps.Keys(unaudited)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("unaudited %+v, want entries for %v (%s); a source file with no coverage record was not audited for coverage, which must not read as clean", got, want, where)
	}
	for lang, paths := range want {
		for _, p := range paths {
			if !strings.Contains(unaudited[lang], p) {
				t.Fatalf("%s unaudited message %q does not name %s (%s)", lang, unaudited[lang], p, where)
			}
		}
	}
}

func checkFindingIDs(t *rapid.T, groups map[string][]AuditFinding, where string) {
	owner := map[string]string{}
	for key, gs := range groups {
		slices.SortFunc(gs, func(a, b AuditFinding) int { return cmp.Compare(occurrence(t, a.ID), occurrence(t, b.ID)) })
		for i, g := range gs {
			base := findingIDRe.FindStringSubmatch(g.ID)[1]
			if n := occurrence(t, g.ID); n != i+1 {
				t.Fatalf("findings with identical content %q carry ids %+v; want the bare id for the first occurrence and -2, -3… for later ones (%s)", key, gs, where)
			}
			if i > 0 && gs[i-1].Line > g.Line {
				t.Fatalf("occurrence suffixes %+v do not follow line order (%s); the first occurrence in the file keeps the bare id", gs, where)
			}
			if first := findingIDRe.FindStringSubmatch(gs[0].ID)[1]; base != first {
				t.Fatalf("findings with identical content %q carry ids %+v with different bases (%s); identical content must hash identically so only the occurrence suffix tells them apart", key, gs, where)
			}
			if prev, ok := owner[base]; ok && prev != key {
				t.Fatalf("id base %s shared by different content %q and %q (%s); the id is derived from kind, path and content, so a destination would merge two distinct weaknesses", base, prev, key, where)
			}
			owner[base] = key
		}
	}
}

func occurrence(t *rapid.T, id string) int {
	m := findingIDRe.FindStringSubmatch(id)
	if m == nil {
		t.Fatalf("finding id %q is not 16 lowercase hex digits with an optional -N suffix", id)
	}
	if m[2] == "" {
		return 1
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

func (d *findingsDraw) insertLine(f *modelFile, at int) {
	shift := func(l int) int {
		if l >= at {
			return l + 1
		}
		return l
	}
	f.lines = slices.Insert(f.lines, at-1, unrelatedLine)
	for i := range f.ranges {
		f.ranges[i] = [2]int{shift(f.ranges[i][0]), shift(f.ranges[i][1])}
	}
	for i := range f.mutants {
		f.mutants[i].line = shift(f.mutants[i].line)
	}
	for i := range f.funcs {
		f.funcs[i].Start, f.funcs[i].End = shift(f.funcs[i].Start), shift(f.funcs[i].End)
	}
	hits := map[int]int{}
	for l, h := range f.hits {
		hits[shift(l)] = h
	}
	f.hits = hits
}

func (f auditFixture) runFindingsAudit(t *rapid.T, d *findingsDraw, where string) AuditFindings {
	putFile(t, f.root, "assurance.yaml", d.manifest())
	m, err := core.LoadManifest(filepath.Join(f.root, "assurance.yaml"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	m.Catalog.Objectives = slices.Clone(m.Catalog.Objectives)
	for i, o := range m.Catalog.Objectives {
		if o.ID != auditMutation {
			continue
		}
		o.Threshold = maps.Clone(o.Threshold)
		for l, drop := range d.dropTh {
			if drop {
				delete(o.Threshold, l)
			}
		}
		m.Catalog.Objectives[i] = o
	}
	for _, lang := range auditTestLangs {
		f.install(t, lang, auditObjectives, d.responses(lang))
	}
	args, scope := d.scopeArgs()

	outcomes, err := Audit(m, slices.Clone(args), scope)

	if err != nil {
		t.Fatalf("Audit: %v (%s)", err, where)
	}
	got := readFindings(t, f.root, where)
	checkFindingsHeader(t, f.root, got, args, where)
	w := d.expect(m)
	checkFindingsMatchModel(t, got, w, where)
	checkMissingRecordOutcomes(t, outcomes, w.unaudited, where)
	return got
}

func checkMissingRecordOutcomes(t *rapid.T, outcomes []AuditOutcome, want map[string][]string, where string) {
	reasons := map[string]string{}
	for _, o := range outcomes {
		if o.Status == auditNotAudited {
			reasons[o.Lang] = o.Reason
		}
	}
	if !slices.Equal(slices.Sorted(maps.Keys(reasons)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("not-audited outcomes %+v, want one for each language in %v (%s); a source file with no coverage record joins the language's not-audited reasons, so the summary line and findings.json say the same thing", outcomes, want, where)
	}
	for lang, paths := range want {
		for _, p := range paths {
			if !strings.Contains(reasons[lang], p) {
				t.Fatalf("%s not-audited reason %q does not name %s (%s)", lang, reasons[lang], p, where)
			}
		}
	}
}

// Each check spawns about 30 adapter and git processes; 100 checks would put the package near the Stop hook's budget.
func fewerChecks(t *testing.T, divisor int) {
	t.Helper()
	old, err := strconv.Atoi(flag.Lookup("rapid.checks").Value.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := flag.Set("rapid.checks", strconv.Itoa(max(1, old/divisor))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = flag.Set("rapid.checks", strconv.Itoa(old)) })
}

func (d *findingsDraw) forceSurvivor(t *rapid.T) (*modelFile, int) {
	var candidates []*modelFile
	for _, mf := range d.files {
		if len(mf.mutants) > 0 && d.levels[mf.dir()] != "D" {
			candidates = append(candidates, mf)
		}
	}
	if len(candidates) == 0 {
		return nil, -1
	}
	mf := rapid.SampledFrom(candidates).Draw(t, "survivor file")
	d.red[mf.lang] = nil
	k := rapid.IntRange(0, len(mf.mutants)-1).Draw(t, "survivor")
	mf.mutants[k].status = "Survived"
	return mf, k
}

func checkIDsKeptAcrossInsert(t *rapid.T, first, second AuditFindings, path string, at int, where string) {
	before := map[[2]string]AuditFinding{}
	for _, g := range first.Findings {
		before[[2]string{g.Path, g.ID}] = g
	}
	for _, g := range second.Findings {
		key := [2]string{g.Path, g.ID}
		old, ok := before[key]
		if !ok {
			t.Fatalf("finding %+v has an id the first audit did not report (%s); an unrelated edit above a weakness must keep its id, or every destination files it again\nbefore %+v\nafter %+v", g, where, first.Findings, second.Findings)
		}
		wantLine := old.Line
		if g.Path == path && old.Line >= at {
			wantLine++
		}
		if g.Line != wantLine || g.Kind != old.Kind || g.Level != old.Level {
			t.Fatalf("id %s moved from %+v to %+v, want line %d (%s)", g.ID, old, g, wantLine, where)
		}
		delete(before, key)
	}
	if len(before) > 0 {
		t.Fatalf("findings %+v disappeared after an unrelated edit (%s)", before, where)
	}
}

func TestAuditFindingsReportEachWeaknessWithAStableContentID(t *testing.T) {
	fewerChecks(t, 5)
	f := newAuditFixture(t)
	rapid.Check(t, func(t *rapid.T) {
		d := drawFindings(t)
		survivor, k := d.forceSurvivor(t)
		d.writeTree(t, f.root)
		commitFixture(t, f.root)

		first := f.runFindingsAudit(t, d, "first audit")
		if first.Dirty {
			t.Fatalf("audit of a freshly committed tree reported dirty; audit's own files under .assure/state/ are not uncommitted edits")
		}

		file := d.files[rapid.IntRange(0, len(d.files)-1).Draw(t, "edited file")]
		at := rapid.IntRange(1, len(file.lines)).Draw(t, "inserted at")
		d.insertLine(file, at)
		putFile(t, f.root, file.path, strings.Join(file.lines, "\n")+"\n")
		where := fmt.Sprintf("after inserting a comment at %s:%d", file.path, at)
		second := f.runFindingsAudit(t, d, where)
		if !second.Dirty {
			t.Fatalf("audit after an uncommitted edit reported a clean tree (%s); the commit alone would not reproduce these findings", where)
		}
		checkIDsKeptAcrossInsert(t, first, second, file.path, at, where)

		if survivor == nil {
			return
		}
		f.checkOwnLineEditChangesID(t, d, survivor, k, second)
	})
}

func (f auditFixture) checkOwnLineEditChangesID(t *rapid.T, d *findingsDraw, mf *modelFile, k int, before AuditFindings) {
	ids := map[string]bool{}
	for _, g := range before.Findings {
		ids[g.ID] = true
	}
	line := mf.mutants[k].line
	edit := rapid.SampledFrom([]string{"line text", "mutator"}).Draw(t, "own edit")
	if edit == "mutator" {
		mf.mutants[k].mutator = "UNIQUE_MUTATOR"
	} else {
		mf.lines[line-1] = "z := unique(x)"
	}
	putFile(t, f.root, mf.path, strings.Join(mf.lines, "\n")+"\n")
	after := f.runFindingsAudit(t, d, "after editing the "+edit+" of the mutant at "+fmt.Sprintf("%s:%d", mf.path, line))

	edited := 0
	for _, g := range after.Findings {
		if g.Kind != kindMutant || g.Path != mf.path || g.Line != line || (edit == "mutator" && !strings.Contains(g.Detail, "UNIQUE_MUTATOR")) {
			continue
		}
		edited++
		if ids[g.ID] {
			t.Fatalf("mutant %+v kept id %s after its %s changed; the edited line is a new weakness, or a fixed one, and must not be deduplicated against the old finding", g, g.ID, edit)
		}
	}
	if edited == 0 {
		t.Fatalf("no surviving-mutant finding at %s:%d after the edit: %+v", mf.path, line, after.Findings)
	}
}

func drawValidFindings(t *rapid.T) AuditFindings {
	hex := rapid.StringMatching(`[0-9a-f]{16}`)
	f := AuditFindings{
		Commit: rapid.StringMatching(`[0-9a-f]{40}`).Draw(t, "commit"),
		Dirty:  rapid.Bool().Draw(t, "dirty"),
		Scope:  rapid.SliceOfN(rapid.SampledFrom([]string{".", "p/a.xx", "p/a.xx:1-3"}), 1, 3).Draw(t, "scope"),
	}
	if rapid.Bool().Draw(t, "tool versions") {
		f.ToolVersions = map[string]map[string]string{"xx": {"t": "1"}}
	}
	for i := range rapid.IntRange(0, 4).Draw(t, "findings") {
		g := AuditFinding{ID: hex.Draw(t, "id"), Language: "xx", Detail: "d"}
		switch g.Kind = rapid.SampledFrom([]string{kindMutant, kindUncovered, kindMissingFuzz, kindTestsFailing}).Draw(t, fmt.Sprintf("kind %d", i)); g.Kind {
		case kindMutant, kindUncovered:
			g.Path, g.Line, g.Level = "p/a.xx", rapid.IntRange(1, 9).Draw(t, "line"), "A"
		case kindMissingFuzz:
			g.Path, g.Level = "p/a.xx", "B"
		}
		f.Findings = append(f.Findings, g)
	}
	if rapid.Bool().Draw(t, "score") {
		f.Scores = []AuditScore{{Language: "xx", Level: "A", Detected: 1, Total: 2, Percent: 50, Threshold: 80, Advisory: true}}
	}
	if rapid.Bool().Draw(t, "unaudited") {
		f.Unaudited = []AuditUnaudited{{Language: "yy", Message: "assure-adapter-yy lookup: not found on PATH"}}
	}
	return f
}

var findingsCorruptions = map[string]func(*AuditFindings){
	"version 1":    func(f *AuditFindings) { f.Version = 1 },
	"short commit": func(f *AuditFindings) { f.Commit = f.Commit[:7] },
	"empty scope":  func(f *AuditFindings) { f.Scope = nil },
	"unknown kind": func(f *AuditFindings) {
		f.Findings = append(f.Findings, AuditFinding{ID: "0123456789abcdef", Kind: "bogus", Language: "xx", Detail: "d"})
	},
	"missing-fuzz with a line": func(f *AuditFindings) {
		f.Findings = append(f.Findings, AuditFinding{ID: "0123456789abcdef", Kind: kindMissingFuzz, Language: "xx", Path: "p/a.xx", Line: 3, Level: "A", Detail: "d"})
	},
	"tests-failing with a path": func(f *AuditFindings) {
		f.Findings = append(f.Findings, AuditFinding{ID: "0123456789abcdef", Kind: kindTestsFailing, Language: "xx", Path: "p/a.xx", Detail: "d"})
	},
	"uppercase id": func(f *AuditFindings) {
		f.Findings = append(f.Findings, AuditFinding{ID: "0123456789ABCDEF", Kind: kindTestsFailing, Language: "xx", Detail: "d"})
	},
	"score with no mutants": func(f *AuditFindings) {
		f.Scores = append(f.Scores, AuditScore{Language: "xx", Level: "A", Threshold: 80, Advisory: true})
	},
	"score not advisory": func(f *AuditFindings) {
		f.Scores = append(f.Scores, AuditScore{Language: "xx", Level: "A", Detected: 1, Total: 1, Percent: 100, Threshold: 80})
	},
	"unaudited without message": func(f *AuditFindings) { f.Unaudited = append(f.Unaudited, AuditUnaudited{Language: "xx"}) },
	"uncovered without its line": func(f *AuditFindings) {
		f.Findings = append(f.Findings, AuditFinding{ID: "0123456789abcdef", Kind: kindUncovered, Language: "xx", Path: "p/a.xx", Level: "A", Detail: "d"})
	},
}

func TestAuditFindingsMarshalWritesOnlySchemaValidDocuments(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := drawValidFindings(t)

		data, err := f.Marshal()

		if err != nil {
			t.Fatalf("Marshal(%+v): %v; a well-formed audit must always produce its findings file", f, err)
		}
		if vs, err := schemas.Validate(schemas.AuditFindings, data); err != nil || len(vs) > 0 {
			t.Fatalf("Marshal output violates the schema: %v %v\n%s", vs, err, data)
		}
		var back AuditFindings
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("%v", err)
		}
		want := f
		if want.ToolVersions == nil {
			want.ToolVersions = map[string]map[string]string{}
		}
		if want.Findings == nil {
			want.Findings = []AuditFinding{}
		}
		if want.Scores == nil {
			want.Scores = []AuditScore{}
		}
		if want.Unaudited == nil {
			want.Unaudited = []AuditUnaudited{}
		}
		if !reflect.DeepEqual(back, want) {
			t.Fatalf("Marshal round trip %+v, want %+v; nothing the audit found may be lost on the way to disk\n%s", back, want, data)
		}

		name := rapid.SampledFrom(slices.Sorted(maps.Keys(findingsCorruptions))).Draw(t, "corruption")
		bad := f
		bad.Scope = slices.Clone(f.Scope)
		bad.Findings = slices.Clone(f.Findings)
		bad.Scores = slices.Clone(f.Scores)
		bad.Unaudited = slices.Clone(f.Unaudited)
		findingsCorruptions[name](&bad)

		data, err = bad.Marshal()

		if err == nil || !strings.Contains(err.Error(), string(schemas.AuditFindings)) {
			t.Fatalf("Marshal with %s: error %v, output %s; a findings file the schema rejects must not reach destinations, and the error must name the schema", name, err, data)
		}
	})
}

func TestAuditWithoutAHeadCommitFailsAndWritesNoFindings(t *testing.T) {
	f := newAuditFixture(t)
	for _, lang := range auditTestLangs {
		f.install(t, lang, auditObjectives, responsesFor(lang, auditTests, "green"))
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auditGit(t, root, "init", "-q")
	putFile(t, root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: A\ncomponents: []\n")
	putFile(t, root, "p/a.xx", "x\n")
	m, err := core.LoadManifest(filepath.Join(root, "assurance.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := Audit(m, []string{"p/a.xx"}, []ScopeFile{{Path: "p/a.xx", Lang: "xx", Role: "source"}})

	if err == nil {
		t.Fatalf("Audit in a repository with no commit returned %+v and no error; findings.json names the commit its findings reproduce at, and there is none", got)
	}
	if _, statErr := os.Stat(findingsPath(root)); !os.IsNotExist(statErr) {
		t.Fatalf("findings.json exists after a failed audit (%v); a stale or partial findings file would be read as this audit's result", statErr)
	}
}

func TestAuditLineTextIsTrimmedInsideTheFileAndEmptyOutsideIt(t *testing.T) {
	root := t.TempDir()
	check := 0
	rapid.Check(t, func(t *rapid.T) {
		check++
		n := rapid.IntRange(1, 5).Draw(t, "lines")
		var lines []string
		for i := range n {
			lines = append(lines, rapid.SampledFrom(findingIndents).Draw(t, fmt.Sprintf("indent %d", i+1))+rapid.SampledFrom(append(slices.Clone(findingTexts), "")).Draw(t, fmt.Sprintf("text %d", i+1)))
		}
		content := strings.Join(lines, "\n")
		if rapid.Bool().Draw(t, "trailing newline") {
			content += "\n"
		}
		exists := rapid.IntRange(0, 3).Draw(t, "file exists") > 0
		path := fmt.Sprintf("f%d.xx", check)
		if exists {
			putFile(t, root, path, content)
		}
		la := newLangAudit(root, "xx")
		for line := -1; line <= n+3; line++ {
			want := ""
			if exists && line >= 1 && line <= n {
				want = strings.TrimSpace(lines[line-1])
			}
			if got := la.text(path, line); got != want {
				t.Fatalf("line %d of %q (exists %v): text %q, want %q; a finding's id hashes this text, so an out-of-file line or unreadable file must hash as empty rather than panic or borrow a neighbour", line, content, exists, got, want)
			}
		}
	})
}

func TestAuditMutantsAtFileBoundariesAndPastEOFAreStillFindings(t *testing.T) {
	f := newAuditFixture(t)
	putFile(t, f.root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: A\ncomponents: []\n")
	putFile(t, f.root, "p/a.xx", "dup\n   \n\tdup")
	report := `{"schemaVersion":"1","thresholds":{"high":80,"low":60},"files":{` +
		`"p/a.xx":{"language":"xx","source":"","mutants":[` +
		`{"id":"1","mutatorName":"M","status":"Survived","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":1}}},` +
		`{"id":"2","mutatorName":"M","status":"Survived","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":1}}},` +
		`{"id":"3","mutatorName":"M","status":"NoCoverage","location":{"start":{"line":3,"column":1},"end":{"line":3,"column":1}}},` +
		`{"id":"7","mutatorName":"M","status":"Survived","location":{"start":{"line":7,"column":1},"end":{"line":7,"column":1}}},` +
		`{"id":"9","mutatorName":"M","status":"Survived","location":{"start":{"line":9,"column":1},"end":{"line":9,"column":1}}}]},` +
		`"p/gone.xx":{"language":"xx","source":"","mutants":[` +
		`{"id":"g1","mutatorName":"M","status":"Survived","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":1}}},` +
		`{"id":"g2","mutatorName":"M","status":"Survived","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":1}}}]}}}`
	resp := responsesFor("xx", auditTests, "green")
	resp[auditMutation+".json"] = runResponse("mutation.report", map[string]string{"t": "1"})
	resp[auditMutation+".ev"] = report
	f.install(t, "xx", []string{auditTests, auditMutation}, resp)
	m, err := core.LoadManifest(filepath.Join(f.root, "assurance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	scope := []ScopeFile{{Path: "p/a.xx", Lang: "xx", Role: "source"}, {Path: "p/gone.xx", Lang: "xx", Role: "source"}}

	if _, err := Audit(m, []string{"p"}, scope); err != nil {
		t.Fatalf("Audit: %v; a mutant reported past the end of its file, or in a file that cannot be read, is still a weakness to report", err)
	}

	got := readFindings(t, f.root, "boundary mutants")
	ids := map[string]string{}
	for _, g := range got.Findings {
		if g.Kind == kindMutant {
			ids[fmt.Sprintf("%s:%d", g.Path, g.Line)] = g.ID
		}
	}
	base := func(at string) string {
		id, ok := ids[at]
		if !ok {
			t.Fatalf("no surviving-mutant finding at %s; findings %+v", at, got.Findings)
		}
		return id
	}
	groups := [][]string{
		{"p/a.xx:1", "p/a.xx:3"},
		{"p/a.xx:2", "p/a.xx:7", "p/a.xx:9"},
		{"p/gone.xx:1", "p/gone.xx:2"},
	}
	var bases []string
	for _, g := range groups {
		first := base(g[0])
		for i, at := range g {
			want := first
			if i > 0 {
				want = fmt.Sprintf("%s-%d", first, i+1)
			}
			if got := base(at); got != want {
				t.Fatalf("%s has id %s, want %s (group %v, ids %v); the first and last line, a blank line and lines past EOF, and lines of an unreadable file, are hashed from their trimmed text (empty when there is none) so identical content collides and takes an occurrence suffix", at, got, want, g, ids)
			}
		}
		bases = append(bases, first)
	}
	if bases[0] == bases[1] {
		t.Fatalf("line text %q and an empty line share id %s; the mutated line's text is part of the id", "dup", bases[0])
	}
}
