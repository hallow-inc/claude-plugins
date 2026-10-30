package main

import (
	"bytes"
	"encoding/json"
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
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func scopeJSON(t fataler, s adapterkit.Scope) []byte {
	entries := make([]map[string]any, len(s))
	for i, e := range s {
		entries[i] = map[string]any{"path": e.Path}
		if e.Ranges != nil {
			entries[i]["ranges"] = e.Ranges
		}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return data
}

func writeScopeFile(t fataler, dir string, data []byte) string {
	p := filepath.Join(dir, "scope.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	return p
}

func runArgs(args ...string) (int, []byte, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.Bytes(), e.String()
}

func runScoped(t fataler, obj, scope, out string) runResponse {
	code, raw, stderr := runArgs("run", obj, "--scope", scope, "--out", out)
	if code != 0 {
		t.Fatalf("run %s --scope exited %d: %s", obj, code, stderr)
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, raw); err != nil || len(vs) > 0 {
		t.Fatalf("run %s --scope: response %s fails the adapter-run schema: %v %v", obj, raw, err, vs)
	}
	var resp runResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v", err)
	}
	return resp
}

func objectiveNames() []string {
	return slices.Sorted(maps.Keys(runners))
}

func TestRunRejectsBothSelectorsOrNeither(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "app/parse.py"}}))
	for _, obj := range objectiveNames() {
		out := filepath.Join(t.TempDir(), "ev")
		for _, args := range [][]string{
			{"--changed-from", "HEAD", "--scope", scope, "--out", out},
			{"--out", out},
			{"--scope", scope},
			{"--changed-from", "HEAD"},
		} {
			code, stdout, stderr := runArgs(append([]string{"run", obj}, args...)...)
			if code != 2 || len(stdout) != 0 || !strings.Contains(stderr, "usage:") || !strings.Contains(stderr, "--scope") {
				t.Fatalf("%s %q: exit %d stdout %q stderr %q; both selectors name two change sets and neither names none, so either is a usage error (2) printing the usage line with both selectors, never a response", obj, args, code, stdout, stderr)
			}
			if c := pytestCall(t, r.dataOf(".")); c.invoked {
				t.Fatalf("%s %q ran python %q before rejecting; a usage error must cost nothing", obj, args, c.args)
			}
			if adapterkit.Exists(out) {
				t.Fatalf("%s %q created %s; a rejected run leaves no evidence directory", obj, args, out)
			}
		}
	}
}

func TestFailOnBaseWithScopeIsUsageError(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "tests/test_cli.py", Ranges: [][2]int{{1, 2}}}}))
	out := filepath.Join(t.TempDir(), "ev")
	code, stdout, stderr := runArgs("run", "VER-FAIL-ON-BASE", "--scope", scope, "--out", out)
	if code != 2 || len(stdout) != 0 {
		t.Fatalf("VER-FAIL-ON-BASE --scope exit %d stdout %q stderr %q, want 2 and no response; fail-on-base reruns tests on a base commit and a scope has none, so any response reports a comparison that never happened", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--changed-from") {
		t.Fatalf("stderr %q does not name --changed-from; the caller must learn which selector fail-on-base needs", stderr)
	}
	if c := pytestCall(t, r.dataOf(".")); c.invoked || adapterkit.Exists(out) {
		t.Fatalf("VER-FAIL-ON-BASE --scope ran python %q or created %s before refusing", c.args, out)
	}
}

var scopeCorruptions = []string{"reversed range", "zero bound", "empty scope", "unknown key", "truncated", "missing file"}

func drawValidScope(t *rapid.T, paths []string) adapterkit.Scope {
	var s adapterkit.Scope
	for range rapid.IntRange(1, 3).Draw(t, "entries") {
		e := adapterkit.ScopeEntry{Path: rapid.SampledFrom(paths).Draw(t, "path")}
		for range rapid.IntRange(0, 2).Draw(t, "ranges") {
			lo := rapid.IntRange(1, 9).Draw(t, "lo")
			e.Ranges = append(e.Ranges, [2]int{lo, lo + rapid.IntRange(0, 4).Draw(t, "span")})
		}
		s = append(s, e)
	}
	return s
}

func corruptScope(t *rapid.T, s adapterkit.Scope, how string) []byte {
	i := rapid.IntRange(0, len(s)-1).Draw(t, "entry")
	switch how {
	case "reversed range":
		hi := rapid.IntRange(2, 9).Draw(t, "hi")
		s[i].Ranges = append(s[i].Ranges, [2]int{hi, rapid.IntRange(1, hi-1).Draw(t, "lo")})
	case "zero bound":
		s[i].Ranges = append(s[i].Ranges, [2]int{0, rapid.IntRange(0, 3).Draw(t, "end")})
	case "empty scope":
		s = adapterkit.Scope{}
	}
	data := scopeJSON(t, s)
	switch how {
	case "unknown key":
		data = bytes.Replace(data, []byte(`{"path"`), []byte(`{"lines":[1],"path"`), 1)
	case "truncated":
		data = data[:rapid.IntRange(0, len(data)-1).Draw(t, "cut")]
	}
	return data
}

func TestMalformedScopeIsUsageError(t *testing.T) {
	r := newPyRepo(t, map[string]string{".": "3.13.9"})
	paths := []string{"app/parse.py", "tests/test_cli.py", "pyproject.toml"}
	valid := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "tests/test_cli.py", Ranges: [][2]int{{1, 1}}}}))
	replayPytest(t, r.dataOf("."), pyReplay{report: xunit1()})
	if code, _, stderr := runArgs("run", "VER-TESTS-PASS", "--scope", valid, "--out", t.TempDir()); code != 0 {
		t.Fatalf("well-formed scope exited %d: %s; the property below needs a scope the adapter accepts to mean anything", code, stderr)
	}
	replayPytest(t, r.dataOf("."), pyReplay{noReport: true})
	rapid.Check(t, func(rt *rapid.T) {
		how := rapid.SampledFrom(scopeCorruptions).Draw(rt, "corruption")
		data := corruptScope(rt, drawValidScope(rt, paths), how)
		dir := t.TempDir()
		file := writeScopeFile(rt, dir, data)
		if how == "missing file" {
			file = filepath.Join(dir, "absent.json")
		}
		obj := rapid.SampledFrom(objectiveNames()).Draw(rt, "objective")
		out := filepath.Join(dir, "ev")
		code, stdout, stderr := runArgs("run", obj, "--scope", file, "--out", out)
		if code != 2 || len(stdout) != 0 || stderr == "" {
			rt.Fatalf("%s with %s scope %s: exit %d stdout %q stderr %q; a scope the schema or range check rejects is a usage error (2) on stderr, never a run over a guessed selection", obj, how, data, code, stdout, stderr)
		}
		if c := pytestCall(rt, r.dataOf(".")); c.invoked || adapterkit.Exists(out) {
			rt.Fatalf("%s with %s scope ran python %q or created %s before rejecting", obj, how, c.args, out)
		}
	})
}

func scopedSelectionRepo(t *testing.T) pyRepo {
	r := newPyRepo(t, map[string]string{".": "3.13.9", "svc/b": "3.12.4"})
	writeTree(t, r.top, map[string]string{
		"app/stub.pyi":          "def f() -> int: ...\n",
		"svc/b/app/y.py":        "Y = 1\n",
		"svc/b/tests/test_y.py": "def test_y():\n    pass\n",
		"svc/b/app/gen.py":      "# Code generated by gen. DO NOT EDIT.\nG = 1\n",
		"svc/b/conftest.py":     "",
	})
	git(t, r.top, "add", "-A")
	git(t, r.top, "commit", "-q", "-m", "more")
	return r
}

func TestScopedUnchangedFileSelectsAsIfChanged(t *testing.T) {
	candidates := []string{
		"app/parse.py", "tests/test_cli.py", "app/stub.pyi", "README.md", "pyproject.toml", "uv.lock",
		"svc/b/pyproject.toml", "svc/b/app/y.py", "svc/b/tests/test_y.py", "svc/b/app/gen.py", "svc/b/conftest.py",
	}
	r := scopedSelectionRepo(t)
	rapid.Check(t, func(rt *rapid.T) {
		picked := rapid.SliceOfNDistinct(rapid.SampledFrom(candidates), 1, len(candidates), rapid.ID[string]).Draw(rt, "scoped")
		var s adapterkit.Scope
		for _, p := range picked {
			e := adapterkit.ScopeEntry{Path: p}
			if rapid.Bool().Draw(rt, "ranged") {
				e.Ranges = [][2]int{{1, 1}}
			}
			s = append(s, e)
		}
		paths, err := selectedPaths(r.top, adapterkit.RunArgs{Scope: s})
		if err != nil {
			rt.Fatalf("%v", err)
		}
		scoped, msg, err := selectUnits(r.top, r.top, paths)
		if err != nil || msg != "" {
			rt.Fatalf("scoped selection: %v %q", err, msg)
		}
		orig := map[string][]byte{}
		for _, p := range picked {
			full := filepath.Join(r.top, filepath.FromSlash(p))
			b, err := os.ReadFile(full)
			if err != nil {
				rt.Fatalf("%v", err)
			}
			orig[full] = b
			if err := os.WriteFile(full, append(slices.Clone(b), "\n# edited\n"...), 0o644); err != nil {
				rt.Fatalf("%v", err)
			}
		}
		changed, err := adapterkit.ChangedPaths(r.top, "HEAD")
		for full, b := range orig {
			if werr := os.WriteFile(full, b, 0o644); werr != nil {
				rt.Fatalf("%v", werr)
			}
		}
		if err != nil {
			rt.Fatalf("%v", err)
		}
		want, _, err := selectUnits(r.top, r.top, changed)
		if err != nil {
			rt.Fatalf("%v", err)
		}
		if !reflect.DeepEqual(scoped, want) {
			rt.Fatalf("scope %v on a clean tree selects %v; editing the same files selects %v; a file listed in --scope must be selected exactly as if it had changed, or an audit of committed code skips the units it names", picked, scoped, want)
		}
	})
}

func TestScopedTestsIgnoreTheDiff(t *testing.T) {
	r := newPyRepo(t, map[string]string{"svc/a": "3.13.9", "svc/b": "3.13.9"})
	writeTree(t, r.top, map[string]string{"svc/b/app/parse.py": "def parse(s):\n    return int(s) + 1\n"})
	replayPytest(t, r.dataOf("svc/a"), pyReplay{report: xunit1(passCase)})
	replayPytest(t, r.dataOf("svc/b"), pyReplay{report: xunit1(passCase)})
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "svc/a/app/parse.py", Ranges: [][2]int{{1, 1}}}}))
	out := t.TempDir()
	junitOf(t, out, runScoped(t, "VER-TESTS-PASS", scope, out))
	if a := pytestCall(t, r.dataOf("svc/a")); !a.invoked || a.cwd != filepath.Join(r.top, "svc", "a") {
		t.Fatalf("svc/a pytest call %+v; the scoped svc/a/app/parse.py is unchanged, yet its unit's suite must run", a)
	}
	if b := pytestCall(t, r.dataOf("svc/b")); b.invoked {
		t.Fatalf("svc/b pytest ran with %q; svc/b changed but lies outside the scope, and a scoped run ignores the diff", b.args)
	}
}

func lineCount(data string) int {
	n := 0
	for range strings.Lines(data) {
		n++
	}
	return n
}

var bodyLineGen = rapid.SampledFrom([]string{"", "x = 1", "  pass", "# é", "\r", "def f(): return 2"})

func drawBody(t *rapid.T) string {
	lines := rapid.SliceOfN(bodyLineGen, 0, 6).Draw(t, "lines")
	body := strings.Join(lines, "\n")
	if rapid.Bool().Draw(t, "trailingNewline") {
		body += "\n"
	}
	return body
}

func TestWholeFileSpansEveryLine(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		rel := fmt.Sprintf("app/m%d.py", rapid.IntRange(0, 1<<30).Draw(t, "name"))
		missing := rapid.Bool().Draw(t, "missing")
		body := ""
		if !missing {
			body = drawBody(t)
			writeTree(t, root, map[string]string{rel: body})
			defer func() { _ = os.Remove(filepath.Join(root, filepath.FromSlash(rel))) }()
		}
		tg, ok, err := wholeFile(root, rel)
		if err != nil {
			t.Fatalf("wholeFile(%q): %v; a missing file is skipped, not an error that ends the run", rel, err)
		}
		n := lineCount(body)
		if ok != (n > 0) {
			t.Fatalf("body %q (missing %v): ok %v, want %v; a file with no lines has nothing to mutate and a file with any line, terminated or not, does", body, missing, ok, n > 0)
		}
		if ok && (tg.rel != rel || !slices.Equal(tg.ranges, []lineRange{{1, n}})) {
			t.Fatalf("body %q: target %+v, want %s lines [1, %d]; a scoped file is mutated whole, so a short range skips the lines the audit asked about and a long one has no line to mutate", body, tg, rel, n)
		}
	})
}

type targetKind string

const (
	kindTracked   targetKind = "tracked source"
	kindUntracked targetKind = "untracked source"
	kindIgnored   targetKind = "ignored source"
	kindMissing   targetKind = "missing source"
	kindTest      targetKind = "test"
	kindGenerated targetKind = "generated"
)

var targetKinds = []targetKind{kindTracked, kindUntracked, kindIgnored, kindMissing, kindTest, kindGenerated}

type wantTarget struct {
	rel             string
	lines           int
	whole, optional bool
}

type drawnTargets struct {
	sel                  []string
	scope                adapterkit.Scope
	want                 []wantTarget
	tracked, uncommitted map[string]string
}

func (d *drawnTargets) addScope(t *rapid.T, p string) bool {
	whole := false
	for range rapid.IntRange(1, 2).Draw(t, "entries") {
		e := adapterkit.ScopeEntry{Path: p}
		if rapid.Bool().Draw(t, "wholeEntry") {
			whole = true
		} else {
			for range rapid.IntRange(1, 2).Draw(t, "ranges") {
				lo := rapid.IntRange(1, 8).Draw(t, "lo")
				e.Ranges = append(e.Ranges, [2]int{lo, lo + rapid.IntRange(0, 4).Draw(t, "span")})
			}
		}
		d.scope = append(d.scope, e)
	}
	return whole
}

func (d *drawnTargets) add(t *rapid.T, iter, j int) {
	kind := rapid.SampledFrom(targetKinds).Draw(t, "kind")
	body := drawBody(t)
	p := fmt.Sprintf("app/f%d_%d.py", iter, j)
	switch kind {
	case kindIgnored:
		p = fmt.Sprintf("app/ignored/f%d_%d.py", iter, j)
	case kindTest:
		p = fmt.Sprintf("tests/test_f%d_%d.py", iter, j)
	case kindGenerated:
		body = "# Code generated by gen. DO NOT EDIT.\n" + body
	}
	d.sel = append(d.sel, p)
	switch kind {
	case kindTracked:
		d.tracked[p] = body
	case kindUntracked, kindIgnored, kindTest, kindGenerated:
		d.uncommitted[p] = body
	}
	whole := d.addScope(t, p)
	n := lineCount(body)
	if (kind == kindTracked || kind == kindUntracked) && (n > 0 || !whole) {
		d.want = append(d.want, wantTarget{rel: p, lines: n, whole: whole, optional: n == 0})
	}
}

func checkScopedTarget(t *rapid.T, s adapterkit.Scope, w wantTarget, got mutationTarget) {
	if !got.scoped {
		t.Fatalf("target %+v is not marked scoped; a --scope run keeps every mutant starting in range wherever it ends, and only the scoped flag tells mutantsFromDump to", got)
	}
	for l := 0; l <= scopedMaxLine+1; l++ {
		want := l >= 1 && l <= w.lines
		if !w.whole {
			want = s.Contains(w.rel, l)
		}
		if inLineRanges(got.ranges, l) != want {
			t.Fatalf("%s (%d lines, whole entry %v) under scope %+v: target ranges %v put line %d in range %v, want %v; a ranged entry mutates only its ranges, so the tests selected and the mutants executed are the ones the audit asked about, while an entry without ranges is the whole file", w.rel, w.lines, w.whole, s, got.ranges, l, !want, want)
		}
	}
}

func checkRefTargetsNotScoped(t *rapid.T, top string, d drawnTargets) {
	edited := map[string]string{}
	for p, body := range d.tracked {
		edited[p] = body + "\nz = 0\n"
	}
	writeTree(t, top, edited)
	ref, err := mutationTargets(runContext{root: top, top: top, ref: "HEAD", sel: map[string][]string{top: d.sel}}, top)
	writeTree(t, top, d.tracked)
	if err != nil {
		t.Fatalf("mutationTargets in ref mode: %v", err)
	}
	var rels []string
	for _, tg := range ref {
		rels = append(rels, tg.rel)
		if tg.scoped || len(tg.ranges) == 0 {
			t.Fatalf("ref-mode target %+v; a --changed-from target carries its diff hunks and is not scoped, so a mutant reaching an unchanged line is dropped", tg)
		}
	}
	if want := slices.Sorted(maps.Keys(d.tracked)); !slices.Equal(slices.Sorted(slices.Values(rels)), want) {
		t.Fatalf("ref mode with every tracked file edited targeted %q, want %q", rels, want)
	}
}

func TestScopedMutationTargetsTakeScopeRanges(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{".gitignore": "app/ignored/\n", "pyproject.toml": "[project]\nname = \"x\"\n"})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	t.Chdir(top)
	iter := 0
	rapid.Check(t, func(rt *rapid.T) {
		iter++
		d := drawnTargets{tracked: map[string]string{}, uncommitted: map[string]string{}, scope: adapterkit.Scope{{Path: "app/other.py", Ranges: [][2]int{{1, 1}}}}}
		for j := range rapid.IntRange(0, 6).Draw(rt, "files") {
			d.add(rt, iter, j)
		}
		writeTree(rt, top, d.tracked)
		git(rt, top, "add", "-A")
		git(rt, top, "commit", "-q", "--allow-empty", "-m", fmt.Sprint(iter))
		writeTree(rt, top, d.uncommitted)
		got, err := mutationTargets(runContext{root: top, top: top, ref: "no-such-ref", scope: d.scope, sel: map[string][]string{top: d.sel}}, top)
		if err != nil {
			rt.Fatalf("mutationTargets in scope mode: %v; a scoped run has no ref, so it must not ask git for a diff", err)
		}
		i := 0
		for _, w := range d.want {
			if i < len(got) && got[i].rel == w.rel {
				checkScopedTarget(rt, d.scope, w, got[i])
				i++
				continue
			}
			if !w.optional {
				rt.Fatalf("selected %q under scope %+v gave targets %+v, missing %s; every tracked or untracked, not ignored source file the scope names is a target, a whole-file entry needing at least one line", d.sel, d.scope, got, w.rel)
			}
		}
		if i != len(got) {
			rt.Fatalf("selected %q under scope %+v gave targets %+v; %+v are ignored, missing, test, generated or empty whole-file targets, none of which a scoped run mutates", d.sel, d.scope, got, got[i:])
		}
		ref, err := mutationTargets(runContext{root: top, top: top, ref: "HEAD", sel: map[string][]string{top: d.sel}}, top)
		if err != nil {
			rt.Fatalf("mutationTargets in ref mode: %v", err)
		}
		if len(ref) != 0 {
			rt.Fatalf("ref mode on the same selection targeted %+v; every tracked file matches HEAD and the rest are untracked, so a diff against HEAD has nothing to mutate, and whole files and untracked files belong to scoped runs only", ref)
		}
		checkRefTargetsNotScoped(rt, top, d)
	})
}

func scopeHas(s adapterkit.Scope, path string, line int) bool {
	for _, e := range s {
		if e.Path != path {
			continue
		}
		if e.Ranges == nil {
			return true
		}
		for _, r := range e.Ranges {
			if r[0] <= line && line <= r[1] {
				return true
			}
		}
	}
	return false
}

func sourceOf(p string) string { return "# " + p + "\n" }

const scopedMaxLine = 12

var scopedPaths = []string{"app/x.py", "app/y.py", "lib/z.py"}

func drawScopedReport(t *rapid.T) (rep strykerReport, all []strykerMutant, owner map[string]string) {
	rep, owner = newStrykerReport(), map[string]string{}
	for _, p := range scopedPaths {
		if !rapid.Bool().Draw(t, "present") {
			continue
		}
		f := &strykerFile{Language: "python", Source: sourceOf(p), Mutants: []strykerMutant{}}
		for i := range rapid.IntRange(0, 6).Draw(t, "mutants") {
			m := strykerMutant{ID: fmt.Sprintf("%s#%d", p, i), MutatorName: "NumberReplacer", Replacement: "1", Status: rapid.SampledFrom([]string{"Killed", "Survived", "NoCoverage"}).Draw(t, "status")}
			m.Location.Start = strykerPosition{Line: rapid.IntRange(1, scopedMaxLine).Draw(t, "start"), Column: 1}
			m.Location.End = strykerPosition{Line: m.Location.Start.Line + rapid.IntRange(0, 4).Draw(t, "span"), Column: 2}
			f.Mutants = append(f.Mutants, m)
			all = append(all, m)
			owner[m.ID] = p
		}
		rep.Files[p] = f
	}
	return rep, all, owner
}

func drawRangedScope(t *rapid.T) adapterkit.Scope {
	var s adapterkit.Scope
	for range rapid.IntRange(1, 3).Draw(t, "entries") {
		e := adapterkit.ScopeEntry{Path: rapid.SampledFrom(append(slices.Clone(scopedPaths), "app/none.py")).Draw(t, "path")}
		if !rapid.Bool().Draw(t, "wholeFile") {
			for range rapid.IntRange(1, 2).Draw(t, "ranges") {
				lo := rapid.IntRange(1, scopedMaxLine).Draw(t, "lo")
				e.Ranges = append(e.Ranges, [2]int{lo, rapid.IntRange(lo, scopedMaxLine).Draw(t, "hi")})
			}
		}
		s = append(s, e)
	}
	return s
}

func TestKeepScopedKeepsExactlyMutantsStartingInScope(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rep, all, owner := drawScopedReport(t)
		s := drawRangedScope(t)
		want := map[string][]string{}
		for _, m := range all {
			if p := owner[m.ID]; scopeHas(s, p, m.Location.Start.Line) {
				want[p] = append(want[p], m.ID)
			}
		}
		rep.keepScoped(s)
		got := map[string][]string{}
		for p, f := range rep.Files {
			if len(f.Mutants) == 0 {
				t.Fatalf("file %s kept with no mutants; a file outside every range would appear in the evidence as audited", p)
			}
			if f.Source != sourceOf(p) || f.Language != "python" {
				t.Fatalf("file %s source %q language %q changed by filtering", p, f.Source, f.Language)
			}
			for _, m := range f.Mutants {
				got[p] = append(got[p], m.ID)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scope %+v kept %v, want %v; a mutant is kept iff its start line is in the scope, so one starting inside but ending past a range is kept (ref mode's within() would drop it), one starting outside is evidence the audit did not ask about, and an entry without ranges is the whole file", s, got, want)
		}
		data, err := rep.encode()
		if err != nil {
			t.Fatalf("filtered report fails the mutation-report schema: %v", err)
		}
		if _, err := ev.ParseMutationReport(data); err != nil {
			t.Fatalf("assure rejects the filtered report: %v\n%s", err, data)
		}
	})
}

// spanBase has a for loop whose iterable spans lines 3-4, so any mutant of
// the iterable starts on line 3 and ends on line 4.
const spanBase = `def first(items):
    total = 0
    for x in (items or
              [7]):
        total = total + x
    return total
`

const spanTests = "from app.span import first\n\n\ndef test_first():\n    assert first([1, 2]) == 3\n"

func TestIntegrationScopedMutation(t *testing.T) {
	files := maps.Clone(mutationRepoFiles)
	files["app/split.py"] = splitChanged
	files["app/span.py"] = spanBase
	files["tests/test_span.py"] = spanTests
	top := realPyRepo(t, files)
	writeTree(t, top, map[string]string{"app/fresh.py": "FRESH = 1 + 1\n"})
	before := repoState(t, top)
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{
		{Path: "app/split.py", Ranges: [][2]int{{8, 8}}},
		{Path: "app/span.py", Ranges: [][2]int{{3, 3}}},
		{Path: "app/fresh.py"},
		{Path: "tests/test_split.py"},
	}))
	out := t.TempDir()
	rep := readMutationReport(t, out, runScoped(t, "VER-MUTATION-CHANGED", scope, out))
	if got := slices.Sorted(maps.Keys(rep.Files)); !slices.Equal(got, []string{"app/fresh.py", "app/span.py", "app/split.py"}) {
		t.Fatalf("files %v, want app/fresh.py, app/span.py and app/split.py; app/cold.py is outside the scope, a test file is never mutated, and the untracked app/fresh.py is named by the scope", got)
	}
	split := rep.Files["app/split.py"]
	if len(split.Mutants) == 0 {
		t.Fatal("no mutants on app/split.py line 8; the scoped, unchanged untested() must be mutated though git sees no change")
	}
	for _, m := range split.Mutants {
		if m.Location.Start.Line != 8 {
			t.Fatalf("app/split.py mutant %+v starts outside line 8; the whole file is mutated and the scope keeps only line 8", m)
		}
	}
	spanning := false
	for _, m := range rep.Files["app/span.py"].Mutants {
		if m.Location.Start.Line != 3 {
			t.Fatalf("app/span.py mutant %+v starts outside line 3", m)
		}
		spanning = spanning || m.Location.End.Line > 3
	}
	if !spanning {
		t.Fatalf("app/span.py mutants %+v; none starts on line 3 and ends on line 4, but the iterable `(items or\\n [7])` does, and a mutant starting in scope is kept wherever it ends", rep.Files["app/span.py"].Mutants)
	}
	if len(rep.Files["app/fresh.py"].Mutants) == 0 {
		t.Fatal("no mutants on the untracked app/fresh.py; a scope entry without ranges is the whole file, tracked or not")
	}
	if after := repoState(t, top); after != before {
		t.Fatalf("the run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

const dumpLineText = "abcdefgh"

type drawnMutant struct{ start, end [2]int }

func drawDumpMutant(t *rapid.T, lines int) drawnMutant {
	sl := rapid.IntRange(1, lines).Draw(t, "startLine")
	el := rapid.IntRange(sl, min(lines, sl+3)).Draw(t, "endLine")
	sc := rapid.IntRange(0, len(dumpLineText)-1).Draw(t, "startCol")
	lo := 0
	if el == sl {
		lo = sc + 1
	}
	return drawnMutant{[2]int{sl, sc}, [2]int{el, rapid.IntRange(lo, len(dumpLineText)).Draw(t, "endCol")}}
}

func dumpLineFor(t fataler, m drawnMutant) string {
	k := m.end[0] - m.start[0] + 1
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- mutation diff ---\n--- a/mod.py\n+++ b/mod.py\n@@ -%d,%d +%d,1 @@\n", m.start[0], k, m.start[0])
	for range k {
		diff.WriteString("-" + dumpLineText + "\n")
	}
	diff.WriteString("+" + dumpLineText[:m.start[1]] + "Z" + dumpLineText[m.end[1]:] + "\n")
	item := map[string]any{"mutations": []map[string]any{{"module_path": "mod.py", "operator_name": "core/NumberReplacer", "start_pos": m.start, "end_pos": m.end}}}
	res := map[string]any{"worker_outcome": "normal", "output": exitSentinel + "1\n", "test_outcome": "killed", "diff": diff.String()}
	data, err := json.Marshal([]any{item, res})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(data)
}

func inLineRanges(rs []lineRange, line int) bool {
	for _, r := range rs {
		if r.start <= line && line <= r.end {
			return true
		}
	}
	return false
}

func TestScopedDumpKeepsMutantsStartingInRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lines := rapid.IntRange(1, 10).Draw(t, "lines")
		orig := strings.Repeat(dumpLineText+"\n", lines)
		var ranges []lineRange
		for range rapid.IntRange(1, 3).Draw(t, "ranges") {
			lo := rapid.IntRange(1, lines).Draw(t, "lo")
			ranges = append(ranges, lineRange{lo, rapid.IntRange(lo, lines).Draw(t, "hi")})
		}
		var dump []string
		var wantScoped, wantRef []strykerPosition
		for range rapid.IntRange(0, 6).Draw(t, "mutants") {
			m := drawDumpMutant(t, lines)
			dump = append(dump, dumpLineFor(t, m))
			start := strykerPosition{m.start[0], m.start[1] + 1}
			if inLineRanges(ranges, m.start[0]) {
				wantScoped = append(wantScoped, start)
			}
			last := m.end[0]
			if m.end[1] == 0 && last > m.start[0] {
				last--
			}
			every := true
			for l := m.start[0]; l <= last; l++ {
				every = every && inLineRanges(ranges, l)
			}
			if every {
				wantRef = append(wantRef, start)
			}
		}
		for _, scoped := range []bool{true, false} {
			muts, err := mutantsFromDump(mutationTarget{rel: "app/mod.py", ranges: ranges, scoped: scoped}, orig, []byte(strings.Join(dump, "\n")), true)
			if err != nil {
				t.Fatalf("scoped %v: %v", scoped, err)
			}
			var got []strykerPosition
			for _, m := range muts {
				got = append(got, m.Location.Start)
			}
			want, why := wantRef, "a --changed-from run keeps a mutant only when every line of its span changed, since a mutant reaching into unchanged code is not evidence about the change"
			if scoped {
				want, why = wantScoped, "a --scope run keeps a mutant whose start line is in a scope range wherever it ends, the rule keepScoped applies, so dropping it here loses mutants the audit asked about"
			}
			if !slices.Equal(got, want) {
				t.Fatalf("scoped %v, ranges %v: kept mutants starting at %v, want %v; %s\ndump:\n%s", scoped, ranges, got, want, why, strings.Join(dump, "\n"))
			}
		}
	})
}

const selSource = "def a(x):\n    return x + 1\n\n\ndef b(x):\n    return x * 2\n"

func selTest(log, name string, arg, want int) string {
	return fmt.Sprintf(`import json

import app.sel


def test_%[2]s():
    with open(app.sel.__file__) as src, open(%[1]q, "a") as out:
        out.write(json.dumps({"test": %[2]q, "source": src.read()}) + "\n")
    assert app.sel.%[2]s(%[3]d) == %[4]d
`, log, name, arg, want)
}

type selRun struct{ Test, Source string }

func readSelLog(t *testing.T, log string) []selRun {
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var runs []selRun
	for line := range strings.Lines(string(data)) {
		var r selRun
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		runs = append(runs, r)
	}
	return runs
}

func changedLines(orig, mutated string) []int {
	o, m := strings.Split(orig, "\n"), strings.Split(mutated, "\n")
	var out []int
	for i := range max(len(o), len(m)) {
		if i >= len(o) || i >= len(m) || o[i] != m[i] {
			out = append(out, i+1)
		}
	}
	return out
}

func TestIntegrationScopedRangeSelectsTestsAndMutants(t *testing.T) {
	log := filepath.Join(t.TempDir(), "runs.jsonl")
	top := realPyRepo(t, map[string]string{
		"app/sel.py":          selSource,
		"tests/test_sel_a.py": selTest(log, "a", 1, 2),
		"tests/test_sel_b.py": selTest(log, "b", 3, 6),
	})
	before := repoState(t, top)
	scope := writeScopeFile(t, t.TempDir(), scopeJSON(t, adapterkit.Scope{{Path: "app/sel.py", Ranges: [][2]int{{2, 2}}}}))
	out := t.TempDir()
	rep := readMutationReport(t, out, runScoped(t, "VER-MUTATION-CHANGED", scope, out))
	runs := readSelLog(t, log)
	counts := map[string]int{}
	mutantRuns := 0
	for _, r := range runs {
		counts[r.Test]++
		lines := changedLines(selSource, r.Source)
		if len(lines) > 0 && !slices.Equal(lines, []int{2}) {
			t.Fatalf("test_%s ran against app/sel.py mutated on lines %v:\n%s\nthe scope names line 2 only, so cosmic-ray's line filter must get the scope's range and never execute a mutant elsewhere; filtering afterwards still pays for every out-of-scope run", r.Test, lines, r.Source)
		}
		if len(lines) > 0 {
			mutantRuns++
		}
	}
	if mutantRuns == 0 {
		t.Fatalf("no test ran against a mutant of line 2 (runs %v); `return x + 1` yields mutants and test_a executes it, so the checks above prove nothing", counts)
	}
	if counts["b"] != 1 {
		t.Fatalf("test_b ran %d times (runs %v), want once, while recording coverage contexts; line 2 is executed only by test_a, so the baseline and every mutant run test_a alone, and running the whole suite per mutant is what makes a scoped audit of a real repository take over half an hour", counts["b"], counts)
	}
	f := rep.Files["app/sel.py"]
	if f == nil || len(f.Mutants) == 0 {
		t.Fatalf("report files %v; app/sel.py line 2 must yield mutants", slices.Sorted(maps.Keys(rep.Files)))
	}
	for _, m := range f.Mutants {
		if m.Location.Start.Line != 2 || m.Status != "Killed" {
			t.Fatalf("mutant %+v; every mutant starts on the scoped line 2 and test_a, asserting a(1) == 2, kills it", m)
		}
	}
	if after := repoState(t, top); after != before {
		t.Fatalf("the run changed the repository\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
