package main

import (
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

const attemptTS = `import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

export function attempt(key: string): number {
  const path = join(process.env.ASSURE_COUNT_DIR ?? ".", Buffer.from(key).toString("hex"));
  const n = existsSync(path) ? Number(readFileSync(path, "utf8")) + 1 : 1;
  writeFileSync(path, String(n));
  return n;
}
`

const (
	tsPass       = "attempt(KEY);"
	tsFailAlways = "throw new Error(`attempt ${attempt(KEY)} fails`);"
	tsFailOnce   = "const n = attempt(KEY);\n    if (n <= 1) throw new Error(`attempt ${n} fails`);"
	tsFailTwice  = "const n = attempt(KEY);\n    if (n <= 2) throw new Error(`attempt ${n} fails`);"
)

type tsCase struct{ title, body string }

// tsTestFile is a vitest file holding cases inside describe(group), or at top level when group is
// empty; each case counts its runs under the key "<file>|<full name>".
func tsTestFile(file, group string, cases ...tsCase) string {
	src := "import { describe, test } from \"vitest\";\nimport { attempt } from \"./attempt\";\n\n"
	indent := ""
	if group != "" {
		src += "describe(" + strconv.Quote(group) + ", () => {\n"
		indent = "  "
	}
	for _, c := range cases {
		full := c.title
		if group != "" {
			full = group + " > " + c.title
		}
		body := strings.ReplaceAll(c.body, "KEY", strconv.Quote(file+"|"+full))
		src += indent + "test(" + strconv.Quote(c.title) + ", () => {\n" + indent + "  " + body + "\n" + indent + "});\n"
	}
	if group != "" {
		src += "});\n"
	}
	return src
}

type tsRerunRepo struct{ top, counts string }

func newTSRerunRepo(t *testing.T, files map[string]string) tsRerunRepo {
	top := realUnit(t)
	counts := t.TempDir()
	t.Setenv("ASSURE_COUNT_DIR", counts)
	files["src/attempt.ts"] = attemptTS
	writeTree(t, top, files)
	return tsRerunRepo{top: top, counts: counts}
}

func (r tsRerunRepo) evidence(t *testing.T) (adapterkit.XMLSuites, ev.JUnit, []byte) {
	out := t.TempDir()
	j := junitIn(t, out, runIn(t, "VER-TESTS-PASS", out))
	data, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var s adapterkit.XMLSuites
	if err := xml.Unmarshal(data, &s); err != nil {
		t.Fatalf("junit: %v\n%s", err, data)
	}
	return s, j, data
}

func (r tsRerunRepo) ran(t *testing.T, file, full string) int {
	data, err := os.ReadFile(filepath.Join(r.counts, hex.EncodeToString([]byte(file+"|"+full))))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (r tsRerunRepo) ranOnce(t *testing.T, group string, titles []string) {
	for _, title := range titles {
		if n := r.ran(t, group, group+" > "+title); n != 1 {
			t.Errorf("%s > %s ran %d times, want 1; the rerun pattern matches the failing test's full name exactly, so its neighbours in the file, including ones sharing a prefix or matching its unescaped reading, never run again", group, title, n)
		}
	}
}

func tsCaseOf(t *testing.T, s adapterkit.XMLSuites, data []byte, classname, name string) adapterkit.XMLCase {
	var found []adapterkit.XMLCase
	for _, su := range s.Suites {
		for _, c := range su.Cases {
			if c.Classname == classname && c.Name == name {
				found = append(found, c)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one testcase %s %s, found %d; a rerun must update the first run's case, not add another\n%s", classname, name, len(found), data)
	}
	return found[0]
}

func TestIntegrationRerunSelectsByFullName(t *testing.T) {
	flakyTitle := "rejects (empty)+"
	passing := []string{"rejects empty", "rejects (empty)", "rejects", "rejects (empty)+ again", "(empty)+"}
	for i := len(passing); i < 11; i++ {
		passing = append(passing, fmt.Sprintf("case %d", i))
	}
	cases := []tsCase{{flakyTitle, tsFailTwice}}
	for _, p := range passing {
		cases = append(cases, tsCase{p, tsPass})
	}
	r := newTSRerunRepo(t, map[string]string{
		"src/parse.test.ts":  tsTestFile("parse", "parse", cases...),
		"src/always.test.ts": tsTestFile("always", "", tsCase{"fails every round [x|y]", tsFailAlways}),
	})
	s, j, data := r.evidence(t)

	c := tsCaseOf(t, s, data, "src/parse.test.ts", "parse > "+flakyTitle)
	if c.Failure == nil || !strings.Contains(c.Failure.Body, "attempt 1 fails") {
		t.Fatalf("flaky case failure %+v; the first run's failure stays in the evidence\n%s", c.Failure, data)
	}
	if got := flakyProps(c); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("parse > %s assure.flaky %q, want [\"2\"]; it failed on the first run and first rerun and passed on the second, and its name holds regexp metacharacters vitest only matches when escaped\n%s", flakyTitle, got, data)
	}
	if n := r.ran(t, "parse", "parse > "+flakyTitle); n != 3 {
		t.Fatalf("parse > %s ran %d times, want 3 (first run and two reruns)", flakyTitle, n)
	}
	r.ranOnce(t, "parse", passing)
	a := tsCaseOf(t, s, data, "src/always.test.ts", "fails every round [x|y]")
	if a.Failure == nil || len(flakyProps(a)) != 0 || !strings.Contains(a.Failure.Body, "attempt 1 fails") {
		t.Errorf("always-failing case %+v; a test failing every round is written as the first run wrote it", a)
	}
	if n := r.ran(t, "always", "fails every round [x|y]"); n != 4 {
		t.Errorf("always-failing test ran %d times, want 4: the first run and 3 rounds", n)
	}
	i := slices.IndexFunc(j.Failing, func(f ev.Failure) bool { return f.Name == "src/parse.test.ts.parse > "+flakyTitle })
	if i < 0 || j.Failing[i].Flaky != 2 {
		t.Errorf("core reader sees failing %+v; it must read the flaky case as a failure flaky on rerun 2", j.Failing)
	}
	if t.Failed() {
		t.Logf("junit:\n%s", data)
	}
}

func TestIntegrationRerunKeepsSameNamedTestsInTwoFilesApart(t *testing.T) {
	const x, y = "rejects empty", "rejects blank"
	r := newTSRerunRepo(t, map[string]string{
		"src/a.test.ts": tsTestFile("a", "parse", tsCase{x, tsFailAlways}),
		"src/b.test.ts": tsTestFile("b", "parse", tsCase{x, tsPass}),
		"src/c.test.ts": tsTestFile("c", "parse", tsCase{x, tsPass}, tsCase{y, tsFailAlways}),
		"src/d.test.ts": tsTestFile("d", "parse", tsCase{x, tsFailOnce}),
	})
	s, _, data := r.evidence(t)
	full := "parse > " + x

	if got := flakyProps(tsCaseOf(t, s, data, "src/a.test.ts", full)); len(got) != 0 {
		t.Errorf("a.test.ts %s assure.flaky %q; it failed every round, and the same-named test passing in c.test.ts, rerun beside it because c.test.ts holds another failing test, is not a pass of a.test.ts's", full, got)
	}
	if n := r.ran(t, "a", full); n != 4 {
		t.Errorf("a.test.ts %s ran %d times, want 4", full, n)
	}
	if got := flakyProps(tsCaseOf(t, s, data, "src/d.test.ts", full)); !slices.Equal(got, []string{"1"}) {
		t.Errorf("d.test.ts %s assure.flaky %q, want [\"1\"]; it passed in its own file on the first rerun", full, got)
	}
	if n := r.ran(t, "d", full); n != 2 {
		t.Errorf("d.test.ts %s ran %d times, want 2", full, n)
	}
	if n := r.ran(t, "b", full); n != 1 {
		t.Errorf("b.test.ts %s ran %d times, want 1; b.test.ts holds no failing test, so the rerun passes only the failing tests' files and never runs it", full, n)
	}
	cx := tsCaseOf(t, s, data, "src/c.test.ts", full)
	if cx.Failure != nil || len(flakyProps(cx)) != 0 {
		t.Errorf("c.test.ts %s %+v; it passed on the first run and stays a plain pass, whatever a rerun of its file reports for it", full, cx)
	}
	if got := flakyProps(tsCaseOf(t, s, data, "src/c.test.ts", "parse > "+y)); len(got) != 0 {
		t.Errorf("c.test.ts parse > %s assure.flaky %q; it failed every round", y, got)
	}
	if t.Failed() {
		t.Logf("junit:\n%s", data)
	}
}
