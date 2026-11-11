package main

import (
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

const attemptSrc = `package PKG

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

var process = time.Now().UnixNano()

func attempt(t *testing.T) int {
	t.Helper()
	name := fmt.Sprintf("%x.count", t.Name())
	data, _ := os.ReadFile(name)
	n, _ := strconv.Atoi(string(data))
	n++
	if err := os.WriteFile(name, []byte(strconv.Itoa(n)), 0o644); err != nil {
		t.Fatal(err)
	}
	return n
}

func logOrder(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile("order.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintf(f, "%d %s %s\n", process, flag.Lookup("test.shuffle").Value.String(), t.Name())
}

var _ = logOrder
`

func attemptFile(pkg string) string {
	return strings.Replace(attemptSrc, "package PKG", "package "+pkg, 1)
}

func testFunc(name, body string) string {
	return "\nfunc " + name + "(t *testing.T) {\n" + body + "}\n"
}

const (
	passOnce     = "\tattempt(t)\n"
	failAlways   = "\tt.Fatalf(\"attempt %d fails\", attempt(t))\n"
	failTwice    = "\tif n := attempt(t); n <= 2 {\n\t\tt.Fatalf(\"attempt %d fails\", n)\n\t}\n"
	orderedTwice = "\tlogOrder(t)\n" + failTwice
)

func rerunModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := committedModule(t, map[string]string{"README.md": "x\n"})
	writeFiles(t, dir, files)
	return dir
}

func rerunJUnit(t *testing.T, dir string) (adapterkit.XMLSuites, ev.JUnit, []byte) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s; a failing or flaky test is evidence for the core to judge, so the adapter must still exit 0", code, stderr)
	}
	if len(resp.Evidence) != 1 {
		t.Fatalf("response %+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	var s adapterkit.XMLSuites
	if err := xml.Unmarshal(data, &s); err != nil {
		t.Fatalf("junit: %v\n%s", err, data)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("junit: %v\n%s", err, data)
	}
	return s, j, data
}

func caseOf(t *testing.T, s adapterkit.XMLSuites, data []byte, classname, name string) adapterkit.XMLCase {
	t.Helper()
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

func flakyOf(c adapterkit.XMLCase) []string {
	var vs []string
	for _, p := range c.Properties {
		if p.Name == "assure.flaky" {
			vs = append(vs, p.Value)
		}
	}
	return vs
}

func executions(t *testing.T, dir, pkg, test string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, pkg, fmt.Sprintf("%x.count", test)))
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

func TestRerunMarksATestThatPassesOnTheSecondRerunAsFlaky(t *testing.T) {
	dir := rerunModule(t, map[string]string{
		"f/count_test.go": attemptFile("f"),
		"f/f_test.go":     "package f\n\nimport \"testing\"\n" + testFunc("TestFlaky", failTwice) + testFunc("TestSteady", passOnce),
	})
	s, j, data := rerunJUnit(t, dir)
	c := caseOf(t, s, data, "example.com/m/f", "TestFlaky")
	if c.Failure == nil || !strings.Contains(c.Failure.Body, "attempt 1 fails") {
		t.Fatalf("TestFlaky failure %+v; a flaky test still failed once, and the first run's failure must stay in the evidence so the human sees what went wrong\n%s", c.Failure, data)
	}
	if got := flakyOf(c); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("TestFlaky assure.flaky %q, want [\"2\"]; it failed on the first run and first rerun and passed on the second, and without the mark the core sends the agent after a flake it did not cause\n%s", got, data)
	}
	if n := executions(t, dir, "f", "TestFlaky"); n != 3 {
		t.Fatalf("TestFlaky ran %d times, want 3 (first run and two reruns); the flaky mark must come from real re-executions, not a cached result", n)
	}
	if n := executions(t, dir, "f", "TestSteady"); n != 1 {
		t.Fatalf("TestSteady ran %d times, want 1; a passing test is never rerun", n)
	}
	i := slices.IndexFunc(j.Failing, func(f ev.Failure) bool { return strings.HasSuffix(f.Name, "TestFlaky") })
	if i < 0 || j.Failing[i].Flaky != 2 {
		t.Fatalf("core reader sees failing %+v; it must read TestFlaky as a failure flaky on rerun 2", j.Failing)
	}
}

func TestRerunLeavesATestThatFailsEveryRoundUnmarked(t *testing.T) {
	dir := rerunModule(t, map[string]string{
		"a/count_test.go": attemptFile("a"),
		"a/a_test.go":     "package a\n\nimport \"testing\"\n" + testFunc("TestAlways", failAlways),
	})
	s, j, data := rerunJUnit(t, dir)
	c := caseOf(t, s, data, "example.com/m/a", "TestAlways")
	if got := flakyOf(c); len(got) != 0 {
		t.Fatalf("TestAlways assure.flaky %q; a test that fails every round is a real failure, and marking it flaky would hide it from the agent\n%s", got, data)
	}
	if c.Failure == nil || !strings.Contains(c.Failure.Body, "attempt 1 fails") || strings.Contains(c.Failure.Body, "attempt 4 fails") {
		t.Fatalf("TestAlways failure %+v; a test failing every round is written as the first run wrote it\n%s", c.Failure, data)
	}
	if n := executions(t, dir, "a", "TestAlways"); n != 4 {
		t.Fatalf("TestAlways ran %d times, want exactly 4: the first run and 3 rerun rounds, the protocol's fixed bound", n)
	}
	if j.Failures != 1 || j.Failing[0].Flaky != 0 {
		t.Fatalf("core reader sees %+v; want one plain failure", j.Failing)
	}
}

func TestRerunRunsOnlyTheFailingTestsOfAPackage(t *testing.T) {
	src := "package p\n\nimport \"testing\"\n" + testFunc("TestA", failAlways) + testFunc("TestB", failAlways)
	passing := []string{"TestAB", "TestXA", "TestB2", "TestBB", "TestA_B"}
	for i := len(passing); i < 18; i++ {
		passing = append(passing, fmt.Sprintf("TestP%d", i))
	}
	for _, name := range passing {
		src += testFunc(name, passOnce)
	}
	dir := rerunModule(t, map[string]string{"p/count_test.go": attemptFile("p"), "p/p_test.go": src})
	_, _, data := rerunJUnit(t, dir)
	for _, name := range []string{"TestA", "TestB"} {
		if n := executions(t, dir, "p", name); n != 4 {
			t.Errorf("%s ran %d times, want 4; both failing tests of the package are rerun every round", name, n)
		}
	}
	for _, name := range passing {
		if n := executions(t, dir, "p", name); n != 1 {
			t.Errorf("%s ran %d times, want 1; a rerun selects the failing tests by anchored, exact name, and a passing test running beside them is not a rerun of them alone", name, n)
		}
	}
	if t.Failed() {
		t.Logf("junit:\n%s", data)
	}
}

func TestRerunSkipsErroringPackages(t *testing.T) {
	dir := rerunModule(t, map[string]string{
		"a/count_test.go": attemptFile("a"),
		"a/a_test.go":     "package a\n\nimport \"testing\"\n" + testFunc("TestAlways", failAlways),
		"bad/bad_test.go": "package bad\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { var s string = 1; _ = s }\n",
		"tm/tm_test.go": "package tm\n\nimport (\n\t\"os\"\n\t\"strconv\"\n\t\"testing\"\n)\n\n" +
			"func TestMain(m *testing.M) {\n\tdata, _ := os.ReadFile(\"main.count\")\n\tn, _ := strconv.Atoi(string(data))\n" +
			"\t_ = os.WriteFile(\"main.count\", []byte(strconv.Itoa(n+1)), 0o644)\n\tos.Exit(1)\n}\n\nfunc TestNever(t *testing.T) {}\n",
	})
	s, j, data := rerunJUnit(t, dir)
	errored := 0
	for _, su := range s.Suites {
		for _, c := range su.Cases {
			if c.Error == nil {
				continue
			}
			errored++
			if got := flakyOf(c); len(got) != 0 {
				t.Errorf("erroring case %s %s has assure.flaky %q; a package that failed without a failing test is never rerun, so nothing can show it flaky\n%s", c.Classname, c.Name, got, data)
			}
		}
	}
	if errored != 2 {
		t.Fatalf("want the build failure and the TestMain failure as two erroring cases, got %d\n%s", errored, data)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "tm", "main.count"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "1" {
		t.Fatalf("the package failing in TestMain ran %s times, want 1; an erroring package is not rerun, even when another package's failing test triggers rerun rounds", raw)
	}
	if n := executions(t, dir, "a", "TestAlways"); n != 4 {
		t.Fatalf("TestAlways ran %d times, want 4; an erroring package beside it must not stop the failing test's reruns", n)
	}
	if j.Errors != 2 {
		t.Fatalf("core reader sees %d errors, want 2", j.Errors)
	}
}

func TestRerunSelectsAFailingSubtestThroughItsTopLevelTest(t *testing.T) {
	subs := []string{"a+b", "x(y)", "[z]", "^.*$|?", "{1}\\d"}
	body := ""
	for _, s := range subs {
		body += "\tt.Run(" + strconv.Quote(s) + ", func(t *testing.T) {\n\t\tif attempt(t) == 1 {\n\t\t\tt.Fatal(\"first attempt fails\")\n\t\t}\n\t})\n"
	}
	body += "\tt.Run(\"plain\", func(t *testing.T) { attempt(t) })\n"
	dir := rerunModule(t, map[string]string{
		"q/count_test.go": attemptFile("q"),
		"q/q_test.go":     "package q\n\nimport \"testing\"\n" + testFunc("TestParse", body) + testFunc("TestOther", passOnce),
	})
	s, _, data := rerunJUnit(t, dir)
	for _, sub := range subs {
		name := "TestParse/" + sub
		c := caseOf(t, s, data, "example.com/m/q", name)
		if c.Failure == nil {
			t.Errorf("%s has no failure; its first run failed", name)
		}
		if got := flakyOf(c); !slices.Equal(got, []string{"1"}) {
			t.Errorf("%s assure.flaky %q, want [\"1\"]; the subtest passed when its top-level test was rerun, and a subtest name holds regexp metacharacters and `/`, which go test splits on, so the rerun must select TestParse escaped and anchored", name, got)
		}
		if n := executions(t, dir, "q", name); n != 2 {
			t.Errorf("%s ran %d times, want 2", name, n)
		}
	}
	parent := caseOf(t, s, data, "example.com/m/q", "TestParse")
	if parent.Failure == nil || !slices.Equal(flakyOf(parent), []string{"1"}) {
		t.Errorf("TestParse failure %+v flaky %q; go test fails the parent of a failing subtest, and the parent passed in the round that reran it, so it is flaky on rerun 1 too", parent.Failure, flakyOf(parent))
	}
	plain := caseOf(t, s, data, "example.com/m/q", "TestParse/plain")
	if plain.Failure != nil || len(flakyOf(plain)) != 0 {
		t.Errorf("TestParse/plain %+v; it passed on the first run and stays a plain pass", plain)
	}
	if n := executions(t, dir, "q", "TestOther"); n != 1 {
		t.Errorf("TestOther ran %d times, want 1; only TestParse is rerun", n)
	}
	if t.Failed() {
		t.Logf("junit:\n%s", data)
	}
}

type processRun struct {
	shuffle []string
	order   []string
}

func processRuns(t *testing.T, raw []byte) []processRun {
	t.Helper()
	var runs []processRun
	index := map[string]int{}
	for l := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			t.Fatalf("order.log line %q", l)
		}
		i, ok := index[f[0]]
		if !ok {
			i = len(runs)
			index[f[0]] = i
			runs = append(runs, processRun{})
		}
		runs[i].shuffle = append(runs[i].shuffle, f[1])
		runs[i].order = append(runs[i].order, f[2])
	}
	return runs
}

func TestRerunReusesTheFirstRunsShuffleSeed(t *testing.T) {
	names := []string{"TestO1", "TestO2", "TestO3", "TestO4", "TestO5", "TestO6"}
	src := "package o\n\nimport \"testing\"\n"
	for _, n := range names {
		src += testFunc(n, orderedTwice)
	}
	dir := rerunModule(t, map[string]string{"o/count_test.go": attemptFile("o"), "o/o_test.go": src})
	s, _, data := rerunJUnit(t, dir)
	raw, err := os.ReadFile(filepath.Join(dir, "o", "order.log"))
	if err != nil {
		t.Fatal(err)
	}
	runs := processRuns(t, raw)
	if len(runs) != 3 {
		t.Fatalf("tests ran in %d processes, want 3 (first run and two rerun rounds)\n%s\njunit:\n%s", len(runs), raw, data)
	}
	seed := runs[1].shuffle[0]
	if _, err := strconv.ParseInt(seed, 10, 64); err != nil {
		t.Fatalf("rerun ran with -shuffle=%s, want the first run's integer seed\n%s", seed, raw)
	}
	for _, r := range runs[1:] {
		if !slices.Equal(r.order, runs[0].order) {
			t.Fatalf("rerun order %v, first run order %v; go test shuffles before -run filters, so the same seed gives the same relative order, and a rerun under another order can pass or fail for reasons the first run never saw\n%s", r.order, runs[0].order, raw)
		}
		if slices.ContainsFunc(r.shuffle, func(v string) bool { return v != seed }) {
			t.Fatalf("rerun rounds ran with -shuffle values %v and %s; every round reuses the first run's seed\n%s", r.shuffle, seed, raw)
		}
	}
	for _, n := range names {
		if got := flakyOf(caseOf(t, s, data, "example.com/m/o", n)); !slices.Equal(got, []string{"2"}) {
			t.Fatalf("%s assure.flaky %q, want [\"2\"]\n%s", n, got, data)
		}
	}
}
