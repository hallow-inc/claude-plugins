package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

func TestDescribeListsGroup7Objectives(t *testing.T) {
	v, _ := invoke(t, "describe")
	objs, _ := v["objectives"].(map[string]any)
	for obj, mayBeFast := range map[string]bool{"VER-FAIL-ON-BASE": false, "VER-ROBUST-FUZZ": true, "VER-COVERAGE-RESOLUTION": false} {
		o, ok := objs[obj].(map[string]any)
		if !ok {
			t.Fatalf("describe objectives %v lack %s; assure never asks an adapter to run an objective it does not list", objs, obj)
		}
		if fast, _ := o["fast"].(bool); fast && !mayBeFast {
			t.Fatalf("%s is listed fast; it reruns tests on a rebuilt tree or the whole unit, too slow for the Stop hook", obj)
		}
	}
}

var (
	listedFileGen = rapid.SampledFrom([]string{"src/a.test.ts", "src/b.test.ts", "pkg/a.test.ts"})
	listedNameGen = rapid.SampledFrom([]string{"parses", "range > parses", "range > includes the end", "each %i"})
	locationGen   = rapid.Custom(func(t *rapid.T) taskLocation {
		return taskLocation{Line: rapid.IntRange(1, 3).Draw(t, "line"), Column: rapid.IntRange(1, 3).Draw(t, "column")}
	})
	listedGen = rapid.Custom(func(t *rapid.T) listedTest {
		return listedTest{File: listedFileGen.Draw(t, "file"), Name: listedNameGen.Draw(t, "name"), Location: locationGen.Draw(t, "location")}
	})
)

func TestNewTestsAreHeadNamesAbsentAtBase(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		head := rapid.SliceOfN(listedGen, 0, 8).Draw(t, "head")
		base := rapid.SliceOfN(listedGen, 0, 8).Draw(t, "base")
		var want []listedTest
		for _, h := range head {
			if !slices.ContainsFunc(base, func(b listedTest) bool { return b.File == h.File && b.Name == h.Name }) {
				want = append(want, h)
			}
		}
		if got := newTests(head, base); !slices.Equal(got, want) {
			t.Fatalf("newTests(head %+v, base %+v) = %+v, want %+v; a test is new exactly when its file and full name are absent at base (an edited body moves its location but keeps its name, a renamed file makes every test in it new), in head order", head, base, got, want)
		}
	})
}

var assertionGen = rapid.Custom(func(t *rapid.T) vitestAssertion {
	a := vitestAssertion{
		Status:          rapid.OneOf(rapid.SampledFrom(vitestStatuses), unknownStatusGen).Draw(t, "status"),
		FailureMessages: rapid.SliceOfN(xmlTextGen, 0, 2).Draw(t, "failures"),
	}
	if rapid.IntRange(0, 4).Draw(t, "located") > 0 {
		loc := locationGen.Draw(t, "location")
		a.Location = &loc
	}
	return a
})

// specBaseCase is the outcome and error text the spec asks of one selected test, given the
// results vitest reported for its file on the base tree.
func specBaseCase(lt listedTest, f *vitestFile, output string) wantCase {
	name := lt.File + "." + lt.Name
	var matched []vitestAssertion
	if f != nil {
		for _, a := range f.AssertionResults {
			if a.Location != nil && *a.Location == lt.Location {
				matched = append(matched, a)
			}
		}
	}
	if len(matched) == 0 {
		text := output
		if f != nil && strings.TrimSpace(f.Message) != "" {
			text = f.Message
		}
		return wantCase{name: name, outcome: ev.Errored, text: specText(text)}
	}
	var failures []string
	failed, skipped := false, 0
	for _, a := range matched {
		switch {
		case a.Status == "failed":
			failed = true
			failures = append(failures, a.FailureMessages...)
		case slices.Contains(specSkipStatuses, a.Status):
			skipped++
		case a.Status != "passed":
			return wantCase{name: name, outcome: ev.Errored}
		}
	}
	switch {
	case failed:
		return wantCase{name: name, outcome: ev.Failed, text: specText(strings.Join(failures, "\n"))}
	case skipped == len(matched):
		return wantCase{name: name, outcome: ev.Skipped}
	}
	return wantCase{name: name, outcome: ev.Passed}
}

func checkCase(t fataler, j ev.JUnit, want wantCase) {
	got := outcomes(j)[want.name]
	if got != want.outcome {
		t.Fatalf("case %q is %q, want %q (all %v)", want.name, got, want.outcome, outcomes(j))
	}
	if want.text != "" {
		if text := failingText(j, want.name); text != want.text {
			t.Fatalf("case %q text %q, want %q; the developer must see why the test failed or did not run on base", want.name, text, want.text)
		}
	}
}

func TestBaseCaseFollowsResultsAtItsLocation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lt := listedGen.Draw(t, "listed")
		var f *vitestFile
		if rapid.Bool().Draw(t, "reported") {
			f = &vitestFile{
				Message:          rapid.OneOf(rapid.Just(""), rapid.Just(" \n"), xmlTextGen).Draw(t, "message"),
				AssertionResults: rapid.SliceOfN(assertionGen, 0, 5).Draw(t, "assertions"),
			}
		}
		output := xmlTextGen.Draw(t, "output")
		c := baseCase(lt, f, output)
		j := junitOf(t, []xmlSuite{{Name: lt.File, Tests: 1, Cases: []xmlCase{c}}})
		if j.Cases != 1 {
			t.Fatalf("%d cases; each selected test is exactly one case", j.Cases)
		}
		// it.each lists once under its template title but runs as several results sharing the
		// listed location, so only location ties a result to the selected test.
		checkCase(t, j, specBaseCase(lt, f, output))
	})
}

func TestBaseSuitesHoldOnlySelectedTests(t *testing.T) {
	root := realTempDir(t)
	rapid.Check(t, func(t *rapid.T) {
		selected := rapid.SliceOfNDistinct(listedGen, 1, 6, func(l listedTest) string { return l.File + "\x00" + l.Name }).Draw(t, "selected")
		var r vitestReport
		files := map[string]*vitestFile{}
		for _, f := range []string{"src/a.test.ts", "src/b.test.ts", "pkg/a.test.ts", "src/unselected.test.ts"} {
			if rapid.IntRange(0, 3).Draw(t, "has "+f) == 0 {
				continue
			}
			r.TestResults = append(r.TestResults, vitestFile{
				Name:             filepath.Join(root, filepath.FromSlash(f)),
				Message:          rapid.OneOf(rapid.Just(""), xmlTextGen).Draw(t, "message"),
				AssertionResults: rapid.SliceOfN(assertionGen, 0, 6).Draw(t, "assertions"),
			})
		}
		for i := range r.TestResults {
			files[relTo(root, r.TestResults[i].Name)] = &r.TestResults[i]
		}
		report := writeReport(t, root, r)
		broken := rapid.Bool().Draw(t, "broken")
		if broken {
			if err := os.WriteFile(report, []byte("{"), 0o644); err != nil {
				t.Fatal(err)
			}
			files = map[string]*vitestFile{}
		}
		output := xmlTextGen.Draw(t, "output")
		suites := baseSuites(root, selected, report, []byte(output))
		j := junitOf(t, suites)
		if j.Cases != len(selected) {
			t.Fatalf("%d cases for %d selected tests; the evidence holds one case per selected test and none for tests that existed at base", j.Cases, len(selected))
		}
		for _, lt := range selected {
			checkCase(t, j, specBaseCase(lt, files[lt.File], output))
		}
	})
}

// failOnBaseFake installs a vitest that answers "list --json=<path>" and "run
// --outputFile=<path>" from templates in data, with @ROOT@ replaced by its working directory.
// It tells the head unit (headUnit) from the rebuilt base tree by that directory, records
// argv, directory and ASSURE_SEED per subcommand and side, and on a base run copies the
// tree from up (relative to its directory) into data/snap.
func failOnBaseFake(t fataler, dir, data, headUnit, up string) {
	installTool(t, dir, "vitest", "5.0.4")
	exe(t, filepath.Join(dir, "node_modules", ".bin", "vitest"), fmt.Sprintf(`#!/bin/sh
d='%s'
here=$(pwd -P)
if [ "$here" = '%s' ]; then w=head; else w=base; fi
k="$d/$1.$w"
printf '%%s\n' "$@" > "$k.args"
echo "$here" > "$k.cwd"
printf '%%s' "$ASSURE_SEED" > "$k.seed"
if [ "$1.$w" = run.base ]; then /bin/cp -R '%s' "$d/snap"; fi
for a; do case "$a" in
--json=*) /usr/bin/sed "s#@ROOT@#$here#g" "$k.json" > "${a#--json=}";;
--outputFile=*) [ -f "$k.json" ] && /usr/bin/sed "s#@ROOT@#$here#g" "$k.json" > "${a#--outputFile=}";;
--coverage.reportsDirectory=*) [ -f "$d/coverage.json" ] && /bin/mkdir -p "${a#*=}" && /usr/bin/sed "s#@ROOT@#$here#g" "$d/coverage.json" > "${a#*=}/coverage-final.json";;
esac; done
exit $(/bin/cat "$k.code" 2>/dev/null || echo 0)
`, data, headUnit, up))
}

func fileText(t fataler, p string) (string, bool) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b), true
}

type repoState struct{ status, worktrees, index string }

func stateOf(t *testing.T, top string) repoState {
	status := gitStatus(t, top)
	wt, err := exec.CommandContext(t.Context(), "git", "-C", top, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(filepath.Join(top, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	return repoState{status: status, worktrees: string(wt), index: fmt.Sprintf("%x", sha256.Sum256(idx))}
}

func listing(t fataler, entries ...listedTest) string {
	for i := range entries {
		entries[i].File = "@ROOT@/" + entries[i].File
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(data)
}

func failOnBaseIn(t fataler, out string, resp runResponse) ev.JUnit {
	if want := []evidence{{Type: "test.fail_on_base", Path: "fail-on-base.xml"}}; !slices.Equal(resp.Evidence, want) {
		t.Fatalf("evidence %+v, want %+v; VER-FAIL-ON-BASE reads exactly this entry", resp.Evidence, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "fail-on-base.xml"))
	if err != nil {
		t.Fatalf("evidence names fail-on-base.xml but it was not written: %v", err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("assure's JUnit reader rejects the evidence: %v\n%s", err, data)
	}
	return j
}

// failOnBaseMonorepo commits a two-unit repository with vitest hoisted to the top, then
// edits, adds, and deletes test files in packages/api and edits files the base tree must
// take from the base commit instead.
func failOnBaseMonorepo(t *testing.T) (top, data string) {
	isolateGit(t)
	fakeNodeOnPath(t)
	top, data = realTempDir(t), t.TempDir()
	api := filepath.Join(top, "packages", "api")
	writeTree(t, top, map[string]string{
		".gitignore":                     "node_modules\n",
		"package.json":                   "{}",
		"package-lock.json":              "{}",
		"README.md":                      "base readme",
		"tsconfig.base.json":             "{}",
		"docs/guide.md":                  "guide",
		"packages/web/package.json":      "{}",
		"packages/web/src/w.ts":          "export const w = 1;\n",
		"packages/api/package.json":      "{}",
		"packages/api/tsconfig.json":     `{ "extends": "../../tsconfig.base.json" }`,
		"packages/api/src/range.ts":      "base range",
		"packages/api/src/range.test.ts": "base test",
		"packages/api/src/old.test.ts":   "old",
		"packages/api/src/kept.test.ts":  "kept",
	})
	failOnBaseFake(t, top, data, api, "../..")
	writeTree(t, api, map[string]string{"node_modules/local/package.json": "{}"})
	git(t, top, "init", "-q")
	git(t, top, "add", "-A")
	git(t, top, "commit", "-q", "-m", "base")
	writeTree(t, top, map[string]string{
		"README.md":                      "head readme",
		"packages/api/src/range.ts":      "head range",
		"packages/api/src/range.test.ts": "head test",
		"packages/api/src/new.test.ts":   "new",
	})
	if err := os.Remove(filepath.Join(api, "src", "old.test.ts")); err != nil {
		t.Fatal(err)
	}
	parses := listedTest{File: "src/range.test.ts", Name: "range > parses", Location: taskLocation{3, 3}}
	end := listedTest{File: "src/range.test.ts", Name: "range > includes the end", Location: taskLocation{7, 3}}
	fresh := listedTest{File: "src/new.test.ts", Name: "fresh", Location: taskLocation{2, 1}}
	writeTree(t, data, map[string]string{
		"list.head.json": listing(t, parses, end, fresh),
		"list.base.json": listing(t, parses, listedTest{File: "src/old.test.ts", Name: "gone", Location: taskLocation{1, 1}}),
		"run.base.json": `{"testResults": [
  {"name": "@ROOT@/src/range.test.ts", "status": "failed", "message": "", "assertionResults": [
    {"ancestorTitles": ["range"], "title": "parses", "status": "passed", "failureMessages": [], "location": {"line": 3, "column": 3}},
    {"ancestorTitles": ["range"], "title": "includes the end", "status": "failed", "failureMessages": ["expected false to be true"], "location": {"line": 7, "column": 3}}]},
  {"name": "@ROOT@/src/new.test.ts", "status": "failed", "message": "Cannot find module './c' imported from src/new.test.ts", "assertionResults": []}]}`,
		"run.base.code": "1",
	})
	t.Chdir(top)
	return top, data
}

func TestRunFailOnBaseRebuildsBaseTreeAndKeepsSelected(t *testing.T) {
	top, data := failOnBaseMonorepo(t)
	before := stateOf(t, top)

	out := t.TempDir()
	resp := runIn(t, "VER-FAIL-ON-BASE", out)
	if resp.Environment != nil {
		t.Fatalf("environment %q", resp.Environment.Message)
	}
	if resp.ToolVersions["vitest"] != "5.0.4" {
		t.Fatalf("tool_versions %v lack vitest 5.0.4", resp.ToolVersions)
	}
	if after := stateOf(t, top); after != before {
		t.Fatalf("repository state changed from %+v to %+v; building the base tree must not touch the worktree, index, or worktree list", before, after)
	}

	j := failOnBaseIn(t, out, resp)
	want := map[string]ev.Outcome{
		"packages/api/src/range.test.ts.range > includes the end": ev.Failed,
		"packages/api/src/new.test.ts.fresh":                      ev.Errored,
	}
	if got := outcomes(j); len(got) != len(want) || got["packages/api/src/range.test.ts.range > includes the end"] != ev.Failed || got["packages/api/src/new.test.ts.fresh"] != ev.Errored {
		t.Fatalf("cases %v, want %v; the edited range > parses existed at base and must not appear, the new test fails there, and the test whose file cannot load errors", got, want)
	}
	if text := failingText(j, "packages/api/src/new.test.ts.fresh"); !strings.Contains(text, "Cannot find module './c'") {
		t.Fatalf("error text %q; a test needing a new export must carry vitest's load error", text)
	}
	checkBaseRun(t, top, data)
	checkBaseTree(t, top, filepath.Join(data, "snap"))
}

func checkBaseRun(t *testing.T, top, data string) {
	run := callOf(t, data, "run.base")
	baseUnit := run.cwd
	if strings.HasPrefix(baseUnit, top+string(filepath.Separator)) || !strings.HasSuffix(baseUnit, filepath.Join("packages", "api")) {
		t.Fatalf("base run in %s; it must run in the rebuilt tree's packages/api, outside the repository", baseUnit)
	}
	if wantFiles := []string{filepath.Join(baseUnit, "src", "new.test.ts"), filepath.Join(baseUnit, "src", "range.test.ts")}; !slices.Equal(run.files(), wantFiles) {
		t.Fatalf("base run files %v, want %v; only changed test files holding a selected test run", run.files(), wantFiles)
	}
	if seed, _ := fileText(t, filepath.Join(data, "run.base.seed")); seed != checkSeed(t, headOf(t, top)) {
		t.Fatalf("ASSURE_SEED %q, want the seed of HEAD; property tests must replay on base the inputs they used at head", seed)
	}
	if list := callOf(t, data, "list.base"); !slices.Equal(list.files(), []string{filepath.Join(baseUnit, "src", "old.test.ts"), filepath.Join(baseUnit, "src", "range.test.ts")}) {
		t.Fatalf("base listing files %v; base names come from the changed test files as they existed at base", list.files())
	}
}

func checkBaseTree(t *testing.T, top, snap string) {
	for rel, want := range map[string]string{
		"README.md":                      "base readme",
		"tsconfig.base.json":             "{}",
		"packages/api/src/range.ts":      "base range",
		"packages/api/src/kept.test.ts":  "kept",
		"packages/api/src/range.test.ts": "head test",
		"packages/api/src/new.test.ts":   "new",
	} {
		if got, ok := fileText(t, filepath.Join(snap, filepath.FromSlash(rel))); !ok || got != want {
			t.Fatalf("base tree %s = %q (present %v), want %q; the tree is the base commit with only the changed test files taken from the worktree", rel, got, ok, want)
		}
	}
	for _, rel := range []string{"packages/api/src/old.test.ts", "docs", "packages/web"} {
		if _, err := os.Lstat(filepath.Join(snap, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("base tree has %s (%v); a deleted test file is removed, and only the unit's tree and its ancestors' own files are copied", rel, err)
		}
	}
	for _, rel := range []string{"node_modules", "packages/api/node_modules"} {
		if link, err := os.Readlink(filepath.Join(snap, filepath.FromSlash(rel))); err != nil || link != filepath.Join(top, filepath.FromSlash(rel)) {
			t.Fatalf("base tree %s links to %q (%v); every node_modules from unit to top must point at the current install so hoisted packages resolve", rel, link, err)
		}
	}
}

func TestRunFailOnBaseWithoutNewTestsRunsNothing(t *testing.T) {
	r := newRepo(t, map[string]string{"src/cli.test.ts": "base"}, nil)
	failOnBaseFake(t, r.top, r.data, r.top, ".")
	writeTree(t, r.top, map[string]string{"src/cli.test.ts": "edited"})
	cli := listedTest{File: "src/cli.test.ts", Name: "cli > adds one", Location: taskLocation{4, 3}}
	writeTree(t, r.data, map[string]string{"list.head.json": listing(t, cli), "list.base.json": listing(t, cli)})
	out := t.TempDir()
	j := failOnBaseIn(t, out, runIn(t, "VER-FAIL-ON-BASE", out))
	if j.Cases != 0 {
		t.Fatalf("cases %v; a change that only edits existing tests has nothing to check on base", outcomes(j))
	}
	if callOf(t, r.data, "run.base").invoked {
		t.Fatal("vitest ran on the base tree although no test was new")
	}
}
