package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

var packObjectives = map[string]lintPack{
	"CODE-CHECK-RETURNS":   checkReturnsPack,
	"CODE-COMPLEXITY":      complexityPack,
	"CODE-NO-UNSAFE":       noUnsafePack,
	"CODE-RESOURCE-BOUNDS": resourceBoundsPack,
}

func TestDescribeListsPacksWithoutFast(t *testing.T) {
	v, _ := invoke(t, "describe")
	objs, _ := v["objectives"].(map[string]any)
	for obj := range packObjectives {
		o, ok := objs[obj].(map[string]any)
		if !ok {
			t.Fatalf("describe objectives %v lack %s; assure never asks an adapter to run an objective it does not list", objs, obj)
		}
		if fast, _ := o["fast"].(bool); fast {
			t.Fatalf("%s is listed fast; a typed ESLint run over the unit is too slow for the Stop hook's fast check", obj)
		}
	}
}

func TestComplexityOfRoundTrips(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		subject := rapid.StringMatching(`[^\n]{1,40}`).Draw(t, "subject")
		n := rapid.IntRange(0, math.MaxInt).Draw(t, "n")
		msg := fmt.Sprintf("%s has a complexity of %d. Maximum allowed is %d.", subject, n, rapid.IntRange(0, 50).Draw(t, "max"))
		got, m, ok := complexityOf(msg)
		if !ok || got != subject || m != n {
			t.Fatalf("complexityOf(%q) = %q, %d, %v; want %q, %d; the metric is ESLint's number, whatever the function is called", msg, got, m, ok, subject, n)
		}
	})
}

var unrecognizedComplexity = []string{
	"",
	"Function 'f' has complexity 3.",
	"Function 'f' has a complexity of many. Maximum allowed is 0.",
	"Function 'f' has a complexity of 99999999999999999999. Maximum allowed is 0.",
	" has a complexity of 3. Maximum allowed is 0.",
}

var foreignRules = []string{"prefer-const", "no-unused-vars", "@typescript-eslint/no-unused-vars", "eqeqeq"}

type message struct {
	RuleID   *string `json:"ruleId"`
	Severity int     `json:"severity"`
	Message  string  `json:"message"`
	Fatal    bool    `json:"fatal,omitempty"`
	Line     int     `json:"line"`
}

func messageKinds(p lintPack) (own, others, kinds []string) {
	for _, r := range slices.Sorted(maps.Keys(p.rules)) {
		if r != "complexity" && r != "no-restricted-syntax" {
			own = append(own, r)
		}
	}
	for _, q := range packObjectives {
		for r := range q.rules {
			if p.rules[r] == nil && r != "complexity" && r != "no-restricted-syntax" {
				others = append(others, r)
			}
		}
	}
	others = append(slices.Sorted(slices.Values(others)), foreignRules...)
	kinds = []string{"fatal", "null", "foreign"}
	if len(own) > 0 {
		kinds = append(kinds, "own")
	}
	if p.rules["complexity"] != nil {
		kinds = append(kinds, "complexity", "unrecognized")
	}
	if p.rules["no-restricted-syntax"] != nil {
		kinds = append(kinds, "restricted")
	}
	return own, others, kinds
}

func drawMessage(t *rapid.T, p lintPack, uri string) (message, *ev.Result) {
	sev := rapid.IntRange(1, 2).Draw(t, "severity")
	text := rapid.String().Draw(t, "text")
	level := map[int]string{1: "warning", 2: "error"}[sev]
	own, others, kinds := messageKinds(p)
	m := message{Severity: sev, Message: text, Line: 1}
	switch rapid.SampledFrom(kinds).Draw(t, "kind") {
	case "fatal":
		m.Fatal = true
		if rapid.Bool().Draw(t, "fatalHasRule") {
			r := rapid.SampledFrom(append(own, others...)).Draw(t, "fatalRule")
			m.RuleID = &r
		}
		return m, &ev.Result{RuleID: "parse", Level: "error", Message: text, URI: uri}
	case "null":
		return m, nil
	case "own":
		r := rapid.SampledFrom(own).Draw(t, "ownRule")
		m.RuleID = &r
		return m, &ev.Result{RuleID: r, Level: level, Message: text, URI: uri}
	case "foreign":
		r := rapid.SampledFrom(others).Draw(t, "foreignRule")
		m.RuleID = &r
		return m, nil
	case "complexity":
		r := "complexity"
		m.RuleID = &r
		subject := rapid.SampledFrom([]string{"Function 'cx'", "Arrow function", "Method 'm'", "Async function 'w'", "Static method 's'"}).Draw(t, "subject")
		n := rapid.IntRange(0, 10000).Draw(t, "n")
		m.Message = fmt.Sprintf("%s has a complexity of %d. Maximum allowed is 0.", subject, n)
		return m, &ev.Result{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of " + subject, URI: uri, Metric: float64(n), HasMetric: true}
	case "unrecognized":
		r := "complexity"
		m.RuleID = &r
		m.Message = rapid.SampledFrom(unrecognizedComplexity).Draw(t, "unrecognized")
		return m, &ev.Result{RuleID: "complexity", Level: "error", Message: "unrecognized complexity message: " + m.Message, URI: uri}
	default:
		r := "no-restricted-syntax"
		m.RuleID = &r
		id := rapid.OneOf(rapid.SampledFrom([]string{"double-cast", "reflect", "proxy", "fetch-signal"}), rapid.StringMatching(`[a-z][a-z-]{0,11}`)).Draw(t, "id")
		m.Message = id + ": " + text
		return m, &ev.Result{RuleID: id, Level: "error", Message: m.Message, URI: uri}
	}
}

func TestPackResultsFollowSpecMapping(t *testing.T) {
	root := realTempDir(t)
	scratch := t.TempDir()
	names := slices.Sorted(maps.Keys(packObjectives))
	rapid.Check(t, func(t *rapid.T) {
		p := packObjectives[rapid.SampledFrom(names).Draw(t, "objective")]
		files := rapid.SliceOfNDistinct(rapid.StringMatching(`src/[a-c]{1,2}\.ts`), 1, 3, rapid.ID).Draw(t, "files")
		var reported []map[string]any
		want := []ev.Result{}
		for _, f := range files {
			var msgs []message
			for range rapid.IntRange(0, 6).Draw(t, "nmsgs") {
				m, w := drawMessage(t, p, f)
				msgs = append(msgs, m)
				if w != nil {
					want = append(want, *w)
				}
			}
			reported = append(reported, map[string]any{"filePath": filepath.Join(root, filepath.FromSlash(f)), "messages": msgs})
		}
		stdout, err := json.Marshal(reported)
		if err != nil {
			t.Fatalf("%v", err)
		}
		sr := newRun("eslint " + p.name)
		sr.Results = p.results(root, ".", files, stdout, nil, true)
		got := sarifOf(t, scratch, sr)
		sameResults(t, p.name+": parse errors fail closed, a complexity message becomes a note carrying the metric (an unreadable one an error), a restricted-syntax message takes the rule id before its colon, the pack's own rules keep theirs, and messages from no rule (noInlineConfig notices) or a rule outside the pack are dropped", got, want)
	})
}

func TestPackResultsFlagFilesESLintSkipped(t *testing.T) {
	root := realTempDir(t)
	scratch := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		p := packObjectives[rapid.SampledFrom(slices.Sorted(maps.Keys(packObjectives))).Draw(t, "objective")]
		files := rapid.SliceOfNDistinct(rapid.StringMatching(`src/[a-c]{1,2}\.ts`), 0, 5, rapid.ID).Draw(t, "files")
		reported := []map[string]any{}
		want := []ev.Result{}
		for _, f := range files {
			if rapid.Bool().Draw(t, "reported") {
				reported = append(reported, map[string]any{"filePath": filepath.Join(root, filepath.FromSlash(f)), "messages": []any{}})
			} else {
				want = append(want, ev.Result{RuleID: "eslint", Level: "error", URI: f})
			}
		}
		if rapid.Bool().Draw(t, "extra") {
			reported = append(reported, map[string]any{"filePath": filepath.Join(root, "lib", "extra.ts"), "messages": []any{}})
		}
		stdout, err := json.Marshal(reported)
		if err != nil {
			t.Fatalf("%v", err)
		}
		failure := rapid.SampledFrom([]string{"none", "exit", "unreadable"}).Draw(t, "failure")
		ok := failure != "exit"
		if failure == "unreadable" {
			stdout = []byte("Oops! Something went wrong!")
		}
		if failure != "none" {
			want = []ev.Result{{RuleID: "eslint", Level: "error"}}
		}
		sr := newRun("eslint " + p.name)
		sr.Results = p.results(root, "packages/api", files, stdout, []byte("eslint stderr"), ok)
		got := sarifOf(t, scratch, sr)
		if failure != "none" && len(got) == 1 && (!strings.Contains(got[0].Message, "packages/api") || !strings.Contains(got[0].Message, "eslint stderr")) {
			t.Fatalf("failure message %q names neither the unit nor ESLint's stderr; the developer cannot tell what broke", got[0].Message)
		}
		for i := range got {
			got[i].Message = ""
		}
		sameResults(t, fmt.Sprintf("%s, failure %s, files %q: a selected file ESLint did not report was never checked and must be an error at that file, and an ESLint crash or unreadable output one unlocated error, or the pack passes unchecked code", p.name, failure, files), got, want)
	})
}

// eslintCopyingConfig keeps a copy of the --config file because the adapter
// deletes it when the run ends.
const eslintCopyingConfig = `#!/bin/sh
d='%s'
pwd -P > "$d.cwd"
printf '%%s\n' "$@" > "$d.args"
prev=
for a; do [ "$prev" = --config ] && /bin/cat "$a" > "$d.config"; prev=$a; done
[ -f "$d.stdout" ] && /bin/cat "$d.stdout"
[ -f "$d.stderr" ] && /bin/cat "$d.stderr" >&2
exit $(/bin/cat "$d.code" 2>/dev/null || echo 0)
`

var packRecorded = map[string]struct {
	file string
	want []ev.Result
}{
	"CODE-CHECK-RETURNS": {"eslint-check-returns.json", []ev.Result{
		{RuleID: "@typescript-eslint/no-floating-promises", Level: "error", Message: "Promises must be awaited, end with a call to .catch, end with a call to .then with a rejection handler or be explicitly marked as ignored with the `void` operator.", URI: "src/lint.ts"},
	}},
	"CODE-COMPLEXITY": {"eslint-complexity.json", []ev.Result{
		{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of Function 'cx'", URI: "src/lint.ts", Metric: 3, HasMetric: true},
		{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of Arrow function", URI: "src/lint.ts", Metric: 2, HasMetric: true},
		{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of Method 'm'", URI: "src/lint.ts", Metric: 1, HasMetric: true},
		{RuleID: "cyclomatic", Level: "note", Message: "cyclomatic complexity of Async function 'w'", URI: "src/lint.ts", Metric: 2, HasMetric: true},
	}},
	"CODE-NO-UNSAFE": {"eslint-no-unsafe.json", []ev.Result{
		{RuleID: "@typescript-eslint/no-explicit-any", Level: "error", Message: "Unexpected any. Specify a different type.", URI: "src/lint.ts"},
		{RuleID: "reflect", Level: "error", Message: "reflect: Reflect bypasses static typing", URI: "src/lint.ts"},
		{RuleID: "double-cast", Level: "error", Message: "double-cast: an `as unknown as` cast bypasses type checking", URI: "src/lint.ts"},
		{RuleID: "proxy", Level: "error", Message: "proxy: a Proxy changes property access at run time", URI: "src/lint.ts"},
	}},
	"CODE-RESOURCE-BOUNDS": {"eslint-resource-bounds.json", []ev.Result{
		{RuleID: "no-constant-condition", Level: "error", Message: "Unexpected constant condition.", URI: "src/lint.ts"},
		{RuleID: "fetch-signal", Level: "error", Message: "fetch-signal: fetch without an AbortSignal can wait forever", URI: "src/lint.ts"},
	}},
}

func TestPackRunsReplayRecordedESLint(t *testing.T) {
	r := newRepo(t, nil, map[string]map[string]string{".": {"eslint": "10.12.0", "typescript": "6.0.3"}})
	installTool(t, r.top, "typescript-eslint", "8.71.2")
	exe(t, filepath.Join(r.top, "node_modules", ".bin", "eslint"), fmt.Sprintf(eslintCopyingConfig, filepath.Join(r.data, "eslint")))
	writeTree(t, r.top, map[string]string{
		"src/lint.ts":     "changed",
		"src/syntax.ts":   "changed",
		"src/parse.ts":    "changed",
		"src/cli.test.ts": "changed",
		"src/env.d.ts":    "changed",
	})
	names := slices.Sorted(maps.Keys(packObjectives))
	rapid.Check(t, func(t *rapid.T) {
		obj := rapid.SampledFrom(names).Draw(t, "objective")
		code := rapid.IntRange(0, 2).Draw(t, "eslintExit")
		garbage := rapid.Bool().Draw(t, "unreadableOutput")
		stdout := recorded(t, packRecorded[obj].file, r.unreal)
		if garbage {
			stdout = "Oops! Something went wrong!"
		}
		replay(t, r.data, "eslint", replayed{stdout: stdout, stderr: "eslint stderr", code: code})
		_ = os.Remove(filepath.Join(r.data, "eslint.config"))
		out, err := os.MkdirTemp(r.data, "out-*")
		if err != nil {
			t.Fatalf("%v", err)
		}
		resp := runIn(t, obj, out)
		if want := map[string]string{"node": fakeNodeVersion, "eslint": "10.12.0", "typescript": "6.0.3", "typescript-eslint": "8.71.2"}; !maps.Equal(resp.ToolVersions, want) {
			t.Fatalf("%s: tool_versions %v, want %v; the spec requires eslint, typescript-eslint and typescript", obj, resp.ToolVersions, want)
		}
		if want := map[string]string{"eslint": "package-lock.json", "typescript": "package-lock.json", "typescript-eslint": "package-lock.json"}; !maps.Equal(resp.PinnedBy, want) {
			t.Fatalf("%s: pinned_by %v, want %v", obj, resp.PinnedBy, want)
		}
		got := sarifIn(t, out, resp)
		checkPackCall(t, obj, r)

		var want []ev.Result
		if code == 2 || garbage {
			want = []ev.Result{{RuleID: "eslint", Level: "error"}}
		} else {
			want = slices.Concat(packRecorded[obj].want, []ev.Result{
				{RuleID: "parse", Level: "error", Message: "Parsing error: Variable declaration expected.", URI: "src/syntax.ts"},
				{RuleID: "eslint", Level: "error", URI: "src/parse.ts"},
			})
		}
		for i := range got {
			if got[i].RuleID == "eslint" {
				got[i].Message = ""
			}
		}
		sameResults(t, fmt.Sprintf("%s, eslint exit %d, unreadable %v: findings exit 1, clean exit 0; exit 2 or unreadable output is one error; a parse error and a selected file ESLint never reported each fail closed; the noInlineConfig notice is dropped", obj, code, garbage), got, want)
	})
}

func checkPackCall(t fataler, obj string, r tsRepo) {
	c := callOf(t, r.data, "eslint")
	if !c.invoked || c.cwd != r.top {
		t.Fatalf("%s: eslint call %+v; it must run in the unit", obj, c)
	}
	if want := []string{filepath.Join(r.top, "src", "lint.ts"), filepath.Join(r.top, "src", "parse.ts"), filepath.Join(r.top, "src", "syntax.ts")}; !slices.Equal(c.files(), want) {
		t.Fatalf("%s: eslint files %q, want only the selected source-role files %q; test and declaration files are not pack findings", obj, c.files(), want)
	}
	if i := slices.Index(c.args, "--format"); i < 0 || i+1 >= len(c.args) || c.args[i+1] != "json" {
		t.Fatalf("%s: eslint args %q lack --format json, which the converter reads", obj, c.args)
	}
	i := slices.Index(c.args, "--config")
	if i < 0 || i+1 >= len(c.args) || strings.HasPrefix(c.args[i+1], r.top+string(filepath.Separator)) {
		t.Fatalf("%s: eslint args %q; the pack passes its own --config from outside the repository so the repository's ESLint configuration cannot change or disable it", obj, c.args)
	}
	if exists(c.args[i+1]) {
		t.Fatalf("%s: the adapter's config %s outlived the run", obj, c.args[i+1])
	}
	cfg, err := os.ReadFile(filepath.Join(r.data, "eslint.config"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{
		"createRequire(" + jsonString(t, filepath.Join(r.top, "package.json")) + ")(\"typescript-eslint\")",
		"projectService: true",
		"tsconfigRootDir: " + jsonString(t, r.top),
		"noInlineConfig: true",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("%s: config lacks %q; typescript-eslint must load from the unit, typed rules need the unit's tsconfig, and inline disables must not suppress a pack:\n%s", obj, want, cfg)
		}
	}
}

func jsonString(t fataler, s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b)
}

func TestPackRunSkipsUnitsWithoutSourceAndNeedsTsconfig(t *testing.T) {
	isolateGit(t)
	fakeNodeOnPath(t)
	top, data := realTempDir(t), t.TempDir()
	files := map[string]string{".gitignore": "node_modules\n"}
	tools := map[string]map[string]string{}
	for _, unit := range []string{"packages/a", "packages/b"} {
		files[unit+"/package.json"] = "{}"
		files[unit+"/package-lock.json"] = "{}"
		tools[unit] = map[string]string{"eslint": "10.12.0", "typescript": "6.0.3"}
	}
	files["packages/a/src/a.test.ts"] = ""
	files["packages/b/tsconfig.json"] = "{}"
	files["packages/b/src/b.ts"] = ""
	buildRepo(t, top, data, files, tools)
	for unit := range tools {
		installTool(t, filepath.Join(top, filepath.FromSlash(unit)), "typescript-eslint", "8.71.2")
	}
	t.Chdir(top)
	writeTree(t, top, map[string]string{"packages/a/src/a.test.ts": "changed", "packages/b/src/b.ts": "changed"})
	aData, bData := filepath.Join(data, "packages", "a"), filepath.Join(data, "packages", "b")
	bTS := filepath.Join(top, "packages", "b", "src", "b.ts")

	for _, obj := range slices.Sorted(maps.Keys(packObjectives)) {
		replay(t, aData, "eslint", replayed{stdout: "[]"})
		replay(t, bData, "eslint", replayed{stdout: fmt.Sprintf(`[{"filePath":%s,"messages":[]}]`, jsonString(t, bTS))})
		out := t.TempDir()
		resp := runIn(t, obj, out)
		if resp.Environment != nil {
			t.Fatalf("%s: environment %q; packages/a has only a test file changed, so it needs no tsconfig.json and no ESLint run", obj, resp.Environment.Message)
		}
		if rs := sarifIn(t, out, resp); len(rs) != 0 {
			t.Fatalf("%s: results %+v from a clean run", obj, rs)
		}
		if callOf(t, aData, "eslint").invoked {
			t.Fatalf("%s: eslint ran in packages/a, whose only selected file is a test; ESLint with no file argument lints the whole unit", obj)
		}
		if c := callOf(t, bData, "eslint"); !slices.Equal(c.files(), []string{bTS}) {
			t.Fatalf("%s: packages/b eslint call %+v, want its source file", obj, c)
		}
	}

	if err := os.Remove(filepath.Join(top, "packages", "b", "tsconfig.json")); err != nil {
		t.Fatal(err)
	}
	for _, obj := range slices.Sorted(maps.Keys(packObjectives)) {
		replay(t, bData, "eslint", replayed{stdout: "[]"})
		out := t.TempDir()
		resp := runIn(t, obj, out)
		if resp.Environment == nil {
			t.Fatalf("%s: no environment without packages/b/tsconfig.json; the typed rules have no project to read, so a pass would be unchecked", obj)
		}
		mustMention(t, resp.Environment.Message, top, "packages/b/tsconfig.json")
		if entries, _ := os.ReadDir(out); len(resp.Evidence) != 0 || len(entries) != 0 {
			t.Fatalf("%s: evidence %+v, files %v; an environment response writes no evidence", obj, resp.Evidence, entries)
		}
		if callOf(t, bData, "eslint").invoked {
			t.Fatalf("%s: eslint ran without the unit's tsconfig.json", obj)
		}
	}
}
