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

const attemptConftest = `import os

import pytest


@pytest.fixture(autouse=True)
def attempt(request):
    root = os.environ["ASSURE_COUNT_DIR"]
    path = os.path.join(root, request.node.nodeid.encode().hex())
    try:
        with open(path) as f:
            n = int(f.read())
    except FileNotFoundError:
        n = 0
    n += 1
    with open(path, "w") as f:
        f.write(str(n))
    with open(os.path.join(root, "runs.log"), "a") as f:
        f.write("%d\t%s\t%s\t%s\t%s\n" % (
            os.getpid(),
            request.config.getoption("randomly_seed"),
            request.config.getoption("hypothesis_seed"),
            os.environ.get("ASSURE_SEED", ""),
            request.node.nodeid,
        ))
    return n
`

const (
	pyFailAlways = "    assert False, \"attempt %d fails\" % attempt\n"
	pyFailTwice  = "    assert attempt > 2, \"attempt %d fails\" % attempt\n"
	pyFailOnce   = "    assert attempt > 1, \"attempt %d fails\" % attempt\n"
	pyPass       = "    pass\n"
)

func pyTestFunc(name, body string) string {
	return "\n\ndef " + name + "(attempt):\n" + body
}

type rerunRepo struct{ top, counts string }

func newRerunRepo(t *testing.T, files map[string]string) rerunRepo {
	t.Helper()
	top := realPyRepo(t, map[string]string{".gitignore": ".venv/\n.hypothesis/\n__pycache__/\n"})
	counts := t.TempDir()
	t.Setenv("ASSURE_COUNT_DIR", counts)
	if _, ok := files["tests/conftest.py"]; !ok {
		files["tests/conftest.py"] = attemptConftest
	}
	writeTree(t, top, files)
	return rerunRepo{top: top, counts: counts}
}

func (r rerunRepo) evidence(t *testing.T) (adapterkit.XMLSuites, ev.JUnit, []byte) {
	t.Helper()
	out := t.TempDir()
	j := junitOf(t, out, runTestsIn(t, out))
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

func (r rerunRepo) ran(t *testing.T, nodeid string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.counts, hex.EncodeToString([]byte(nodeid))))
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

type execution struct{ pid, randomly, hypothesis, assure, nodeid string }

func (r rerunRepo) executions(t *testing.T) []execution {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.counts, "runs.log"))
	if err != nil {
		t.Fatal(err)
	}
	var out []execution
	for l := range strings.Lines(string(data)) {
		f := strings.Split(strings.TrimSuffix(l, "\n"), "\t")
		if len(f) != 5 {
			t.Fatalf("runs.log line %q", l)
		}
		out = append(out, execution{f[0], f[1], f[2], f[3], f[4]})
	}
	return out
}

func pidsOf(runs []execution, nodeid string) []string {
	var pids []string
	for _, e := range runs {
		if e.nodeid == nodeid && !slices.Contains(pids, e.pid) {
			pids = append(pids, e.pid)
		}
	}
	return pids
}

func pyCaseOf(t *testing.T, s adapterkit.XMLSuites, data []byte, classname, name string) adapterkit.XMLCase {
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

func pyFlakyOf(c adapterkit.XMLCase) []string {
	var vs []string
	for _, p := range c.Properties {
		if p.Name == "assure.flaky" {
			vs = append(vs, p.Value)
		}
	}
	return vs
}

func TestIntegrationRerunMarksATestThatPassesOnTheSecondRerunAsFlaky(t *testing.T) {
	r := newRerunRepo(t, map[string]string{
		"tests/test_flaky.py": pyTestFunc("test_flaky", pyFailTwice) + pyTestFunc("test_steady", pyPass),
	})
	s, j, data := r.evidence(t)
	c := pyCaseOf(t, s, data, "tests/test_flaky.py", "test_flaky")
	if c.Failure == nil || !strings.Contains(c.Failure.Body, "attempt 1 fails") {
		t.Fatalf("test_flaky failure %+v; a flaky test still failed once, and the first run's failure must stay in the evidence so the human sees what went wrong\n%s", c.Failure, data)
	}
	if got := pyFlakyOf(c); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("test_flaky assure.flaky %q, want [\"2\"]; it failed on the first run and first rerun and passed on the second, and without the mark the core sends the agent after a flake it did not cause\n%s", got, data)
	}
	if n := r.ran(t, "tests/test_flaky.py::test_flaky"); n != 3 {
		t.Fatalf("test_flaky ran %d times, want 3 (first run and two reruns); the flaky mark must come from real re-executions of the test", n)
	}
	if n := r.ran(t, "tests/test_flaky.py::test_steady"); n != 1 {
		t.Fatalf("test_steady ran %d times, want 1; a passing test is never rerun, even beside a failing one in the same file", n)
	}
	i := slices.IndexFunc(j.Failing, func(f ev.Failure) bool { return f.Name == "tests/test_flaky.py.test_flaky" })
	if i < 0 || j.Failing[i].Flaky != 2 {
		t.Fatalf("core reader sees failing %+v; it must read test_flaky as a failure flaky on rerun 2", j.Failing)
	}
}

func TestIntegrationRerunLeavesATestThatFailsEveryRoundUnmarked(t *testing.T) {
	r := newRerunRepo(t, map[string]string{
		"tests/test_always.py": pyTestFunc("test_always", pyFailAlways),
	})
	s, j, data := r.evidence(t)
	c := pyCaseOf(t, s, data, "tests/test_always.py", "test_always")
	if got := pyFlakyOf(c); len(got) != 0 {
		t.Fatalf("test_always assure.flaky %q; a test that fails every round is a real failure, and marking it flaky would hide it from the agent\n%s", got, data)
	}
	if c.Failure == nil || !strings.Contains(c.Failure.Body, "attempt 1 fails") || strings.Contains(c.Failure.Body, "attempt 4 fails") {
		t.Fatalf("test_always failure %+v; a test failing every round is written as the first run wrote it\n%s", c.Failure, data)
	}
	if n := r.ran(t, "tests/test_always.py::test_always"); n != 4 {
		t.Fatalf("test_always ran %d times, want exactly 4: the first run and 3 rerun rounds, the protocol's fixed bound", n)
	}
	if j.Failures != 1 || j.Failing[0].Flaky != 0 {
		t.Fatalf("core reader sees %+v; want one plain failure", j.Failing)
	}
}

func TestIntegrationRerunRunsOnlyTheFailingNodeIDsOfAUnit(t *testing.T) {
	cli := "import pytest\n" + pyTestFunc("test_parse", pyFailAlways) + pyTestFunc("test_parse_x", pyPass) + pyTestFunc("test_parsed", pyPass) + pyTestFunc("test_x_test_parse", pyPass) +
		"\n\nclass TestCli:\n    def test_parse(self, attempt):\n        assert False, \"attempt %d fails\" % attempt\n\n    def test_parse_x(self, attempt):\n        pass\n" +
		"\n\nclass TestParse:\n    def test_parse(self, attempt):\n        pass\n"
	other := ""
	for i := range 6 {
		other += pyTestFunc(fmt.Sprintf("test_p%d", i), pyPass)
	}
	r := newRerunRepo(t, map[string]string{
		"tests/test_cli.py":   cli,
		"tests/test_other.py": pyTestFunc("test_parse", pyPass) + other,
	})
	_, _, data := r.evidence(t)
	failing := []string{"tests/test_cli.py::test_parse", "tests/test_cli.py::TestCli::test_parse"}
	passing := []string{
		"tests/test_cli.py::test_parse_x", "tests/test_cli.py::test_parsed", "tests/test_cli.py::test_x_test_parse",
		"tests/test_cli.py::TestCli::test_parse_x", "tests/test_cli.py::TestParse::test_parse",
		"tests/test_other.py::test_parse",
	}
	for i := range 6 {
		passing = append(passing, fmt.Sprintf("tests/test_other.py::test_p%d", i))
	}
	for _, id := range failing {
		if n := r.ran(t, id); n != 4 {
			t.Errorf("%s ran %d times, want 4; every failing test of the unit is rerun each round", id, n)
		}
	}
	for _, id := range passing {
		if n := r.ran(t, id); n != 1 {
			t.Errorf("%s ran %d times, want 1; a rerun selects the failing tests by exact node id, so a test sharing a name, a prefix, or a file with one is not rerun", id, n)
		}
	}
	runs := r.executions(t)
	a, b := pidsOf(runs, failing[0]), pidsOf(runs, failing[1])
	if len(a) != 4 || !slices.Equal(a, b) {
		t.Errorf("the two failing tests ran in processes %v and %v; each round runs pytest once per unit on all of the unit's still-failing tests (design D1), so both share the 4 processes", a, b)
	}
	if t.Failed() {
		t.Logf("junit:\n%s", data)
	}
}

func TestIntegrationRerunSelectsAParametrizedCaseAlone(t *testing.T) {
	ids := []struct{ value, nodeID string }{
		{"b", "b"}, {"a b", "a b"}, {"x::y", "x::y"}, {"p/q", "p/q"}, {"[z]", "[z]"}, {"é", `\xe9`},
	}
	values := []string{strconv.Quote("a")}
	for _, id := range ids {
		values = append(values, strconv.Quote(id.value))
	}
	src := "import pytest\n\n\n@pytest.mark.parametrize(\"v\", [" + strings.Join(values, ", ") + "], ids=lambda v: v)\n" +
		"def test_parse(v, attempt):\n    assert v == \"a\" or attempt > 1, \"attempt %d fails\" % attempt\n" +
		pyTestFunc("test_other", pyPass)
	r := newRerunRepo(t, map[string]string{"tests/test_cli.py": src})
	s, _, data := r.evidence(t)
	for _, id := range ids {
		name := "test_parse[" + id.nodeID + "]"
		c := pyCaseOf(t, s, data, "tests/test_cli.py", name)
		if c.Failure == nil {
			t.Errorf("%s has no failure; its first run failed", name)
		}
		if got := pyFlakyOf(c); !slices.Equal(got, []string{"1"}) {
			t.Errorf("%s assure.flaky %q, want [\"1\"]; it passed on its first rerun, and an id holding a space, `::`, `/`, brackets or an escaped non-ASCII character must still reach pytest as the exact node id", name, got)
		}
		if n := r.ran(t, "tests/test_cli.py::"+name); n != 2 {
			t.Errorf("%s ran %d times, want 2", name, n)
		}
	}
	a := pyCaseOf(t, s, data, "tests/test_cli.py", "test_parse[a]")
	if a.Failure != nil || len(pyFlakyOf(a)) != 0 {
		t.Errorf("test_parse[a] %+v; it passed on the first run and stays a plain pass", a)
	}
	if n := r.ran(t, "tests/test_cli.py::test_parse[a]"); n != 1 {
		t.Errorf("test_parse[a] ran %d times, want 1; a rerun of test_parse[b] selects that case, not the whole parametrized function", n)
	}
	if n := r.ran(t, "tests/test_cli.py::test_other"); n != 1 {
		t.Errorf("test_other ran %d times, want 1", n)
	}
	if t.Failed() {
		t.Logf("junit:\n%s", data)
	}
}

func TestIntegrationRerunSkipsCollectionErrors(t *testing.T) {
	r := newRerunRepo(t, map[string]string{
		"tests/test_always.py": pyTestFunc("test_always", pyFailAlways),
		"tests/test_broken.py": "import os\n\nwith open(os.path.join(os.environ[\"ASSURE_COUNT_DIR\"], \"broken.collected\"), \"a\") as f:\n    f.write(\"x\")\n\nimport module_that_does_not_exist\n",
	})
	s, j, data := r.evidence(t)
	c := pyCaseOf(t, s, data, "tests/test_broken.py", "(collection)")
	if c.Error == nil || len(pyFlakyOf(c)) != 0 {
		t.Fatalf("collection case %+v; a file that failed to collect is an erroring case, never rerun, so nothing can show it flaky\n%s", c, data)
	}
	raw, err := os.ReadFile(filepath.Join(r.counts, "broken.collected"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("tests/test_broken.py was imported %d times, want 1; a file that failed to collect is not rerun, even when another file's failing test triggers rerun rounds in the same unit", len(raw))
	}
	if n := r.ran(t, "tests/test_always.py::test_always"); n != 4 {
		t.Fatalf("test_always ran %d times, want 4; a collection error beside it must not stop the failing test's reruns", n)
	}
	if j.Errors != 1 || j.Failures != 1 {
		t.Fatalf("core reader sees %d errors %d failures, want 1 and 1", j.Errors, j.Failures)
	}
}

func TestIntegrationRerunReusesTheFirstRunsSeed(t *testing.T) {
	r := newRerunRepo(t, map[string]string{
		"tests/test_seeded.py": pyTestFunc("test_seeded", pyFailTwice) + pyTestFunc("test_steady", pyPass),
	})
	r.evidence(t)
	seed := checkSeed(t, headOf(t, r.top))
	runs := r.executions(t)
	if pids := pidsOf(runs, "tests/test_seeded.py::test_seeded"); len(pids) != 3 {
		t.Fatalf("test_seeded ran in processes %v, want 3 (first run and two rerun rounds)", pids)
	}
	for _, e := range runs {
		if e.randomly != seed || e.hypothesis != seed || e.assure != seed {
			t.Fatalf("%s in process %s saw --randomly-seed=%s --hypothesis-seed=%s ASSURE_SEED=%s, want %s for all three; a rerun under another seed orders tests and draws inputs the first run never saw, so its pass says nothing about the first failure", e.nodeid, e.pid, e.randomly, e.hypothesis, e.assure, seed)
		}
	}
}

func TestIntegrationRerunRunsEachUnitInItsOwnRoot(t *testing.T) {
	r := newRerunRepo(t, map[string]string{
		"tests/test_always.py":     pyTestFunc("test_always", pyFailAlways),
		"svc/pyproject.toml":       "[project]\nname = \"svc\"\nversion = \"0\"\nrequires-python = \">=3.11\"\n\n[tool.pytest.ini_options]\npythonpath = [\".\"]\n",
		"svc/tests/conftest.py":    attemptConftest,
		"svc/tests/test_svc.py":    pyTestFunc("test_svc_flaky", pyFailOnce) + pyTestFunc("test_svc_steady", pyPass),
		"svc/tests/test_svc_ok.py": pyTestFunc("test_svc_ok", pyPass),
	})
	pyproject, err := os.ReadFile(filepath.Join(r.top, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	scoped := strings.Replace(string(pyproject), "[tool.pytest.ini_options]\n", "[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n", 1)
	if scoped == string(pyproject) {
		t.Fatal("the synced pyproject.toml has no [tool.pytest.ini_options] table to scope")
	}
	writeTree(t, r.top, map[string]string{"pyproject.toml": scoped})
	s, _, data := r.evidence(t)
	c := pyCaseOf(t, s, data, "svc/tests/test_svc.py", "test_svc_flaky")
	if got := pyFlakyOf(c); c.Failure == nil || !slices.Equal(got, []string{"1"}) {
		t.Fatalf("svc test_svc_flaky failure %+v assure.flaky %q, want a failure flaky on rerun 1; the evidence names it from the manifest root (svc/tests/...) but pytest in unit svc knows it as tests/test_svc.py::test_svc_flaky, and the rerun must select it by the unit's node id\n%s", c.Failure, got, data)
	}
	if n := r.ran(t, "tests/test_svc.py::test_svc_flaky"); n != 2 {
		t.Fatalf("test_svc_flaky ran %d times, want 2", n)
	}
	for _, id := range []string{"tests/test_svc.py::test_svc_steady", "tests/test_svc_ok.py::test_svc_ok"} {
		if n := r.ran(t, id); n != 1 {
			t.Fatalf("svc %s ran %d times, want 1", id, n)
		}
	}
	if n := r.ran(t, "tests/test_always.py::test_always"); n != 4 {
		t.Fatalf("test_always in the top unit ran %d times, want 4; a flake settling in another unit must not end this unit's reruns", n)
	}
}
