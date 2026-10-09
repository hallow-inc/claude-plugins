package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const (
	xxManifestOff = "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n"
	xxManifest    = xxManifestOff + "provenance: true\n"
)

var xxWaivers = func() string {
	var b strings.Builder
	for _, id := range []string{"CODE-ZERO-WARNINGS", "CODE-RESOURCE-BOUNDS", "CODE-CHECK-RETURNS", "CODE-COMPLEXITY", "CODE-NO-UNSAFE",
		"VER-TESTS-PASS", "VER-TRACE-REQ", "VER-MUTATION-CHANGED", "VER-FAIL-ON-BASE", "VER-TEST-BUDGET", "VER-ROBUST-FUZZ", "VER-COVERAGE-RESOLUTION"} {
		b.WriteString("- {objective: " + id + ", scope: '**', rationale: the fake adapter in this test produces no evidence, approver: owner, expires: 2099-01-01}\n")
	}
	return b.String()
}()

type provFixture struct {
	repo
	m *core.Manifest
}

func xxAdapter(t *testing.T, objectives string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":1,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":` + objectives + `}' ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

// provBase commits a base on branch main, runs base to add more to it, then checks out branch pr.
func provBase(t *testing.T, manifest string, base func(f provFixture)) provFixture {
	t.Helper()
	r := newRepo(t, manifest)
	xxAdapter(t, "{}")
	r.write(".gitignore", ".assure/state/\n")
	r.write(".assure/waivers.yaml", xxWaivers)
	r.write("p/x.xx", "one\n")
	r.write("p/x_test.xx", "check one\n")
	r.git("init", "-q", "-b", "main")
	m, err := app.ManifestFor(r.root)
	if err != nil {
		t.Fatal(err)
	}
	f := provFixture{r, m}
	if base != nil {
		base(f)
	}
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	r.git("checkout", "-qb", "pr")
	return f
}

// edit records one file-tool edit through the real pending + record path.
func (f provFixture) edit(session, id, rel, body, agentType string) {
	f.t.Helper()
	if err := app.WritePending(f.m, session, id, rel); err != nil {
		f.t.Fatal(err)
	}
	f.write(rel, body)
	args := []string{"record", "--session", session, "--tool", "Edit", "--tool-use-id", id}
	if agentType != "" {
		args = append(args, "--agent-type", agentType, "--agent-id", "id-"+agentType)
	}
	if code, _, stderr := assure(args...); code != 0 {
		f.t.Fatalf("record %s: exit %d %s", rel, code, stderr)
	}
}

func (f provFixture) commit() {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-qm", "pr")
}

func (f provFixture) evaluate(args ...string) (int, string, string, app.EvalReport) {
	f.t.Helper()
	code, stdout, stderr := assure(append([]string{"evaluate", "--changed-from", "main", "--date", "2026-10-01"}, args...)...)
	if code == 2 {
		return code, stdout, stderr, app.EvalReport{}
	}
	rep, _ := readReport(f.t, f.repo)
	return code, stdout, stderr, rep
}

func (f provFixture) head() string {
	f.t.Helper()
	out, err := exec.CommandContext(f.t.Context(), "git", "-C", f.root, "rev-parse", "HEAD").Output()
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func detailWith(e app.Entry, parts ...string) bool {
	for _, d := range e.Details {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(d, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func independentPR(f provFixture) {
	f.edit("s1", "t1", "p/x.xx", "two\n", core.Implementer)
	f.edit("s1", "t2", "p/x_test.xx", "check two\n", core.Verifier)
}

func (f provFixture) reviews(name, body string) string {
	f.t.Helper()
	p := filepath.Join(f.t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f provFixture) approval(login, commit string) string {
	return f.reviews("reviews.json", fmt.Sprintf(`{"author":"owner","head":%q,"reviews":[{"login":%q,"state":"APPROVED","commit":%q}]}`, f.head(), login, commit))
}

func hasLine(s string, parts ...string) bool {
	for l := range strings.SplitSeq(s, "\n") {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(l, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func (f provFixture) copySession(from, to string) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, core.ProvenanceDir, from))
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(core.ProvenanceDir+"/"+to, string(data))
}

const (
	optionalManifest = xxManifest + "human_review: {B: optional}\n"
	requiredManifest = xxManifest + "human_review: {B: required}\n"
	absent           = "absent"
)

func adoptManifest(f provFixture) {
	f.write("assurance.yaml", xxManifest)
	handEdit(f)
}

type evalScenario struct {
	name     string
	manifest string
	base     func(f provFixture)
	pr       func(f provFixture)
	reviews  func(f provFixture) string
	code     int
	ind, cfg string
	indHas   [][]string
	cfgHas   [][]string
	summary  [][]string
	check    func(t *testing.T, rep app.EvalReport)
}

func (s evalScenario) run(t *testing.T) {
	manifest := s.manifest
	if manifest == "" {
		manifest = xxManifest
	}
	f := provBase(t, manifest, s.base)
	s.pr(f)
	f.commit()
	var args []string
	if s.reviews != nil {
		args = []string{"--reviews", s.reviews(f)}
	}
	code, stdout, stderr, rep := f.evaluate(args...)
	ind, cfg, ok := s.statuses(rep)
	if code != s.code || !ok {
		t.Fatalf("exit %d (want %d), IND %+v (want %s), CFG %+v (want %s), problems %v\n%s%s", code, s.code, ind, s.ind, cfg, s.cfg, rep.Problems, stdout, stderr)
	}
	for _, parts := range s.indHas {
		if !detailWith(ind, parts...) {
			t.Errorf("no IND-VERIFIER-DISTINCT detail has all of %q: %v", parts, ind.Details)
		}
	}
	for _, parts := range s.cfgHas {
		if !detailWith(cfg, parts...) {
			t.Errorf("no CFG-PROTECTED detail has all of %q: %v", parts, cfg.Details)
		}
	}
	for _, parts := range s.summary {
		if !hasLine(stdout, parts...) {
			t.Errorf("no summary line has all of %q:\n%s", parts, stdout)
		}
	}
	if s.check != nil {
		s.check(t, rep)
	}
}

func (s evalScenario) statuses(rep app.EvalReport) (ind, cfg app.Entry, ok bool) {
	ind, indIn := entry(rep, "IND-VERIFIER-DISTINCT", "")
	cfg, _ = entry(rep, "CFG-PROTECTED", "")
	indOK := s.ind == "" || string(ind.Status) == s.ind || (s.ind == absent && !indIn)
	return ind, cfg, indOK && (s.cfg == "" || string(cfg.Status) == s.cfg)
}

func handEdit(f provFixture) { f.write("p/x.xx", "hand edit\n") }

func unrecordedPR(f provFixture) {
	independentPR(f)
	implementerTest(f)
	if _, err := os.Stat(filepath.Join(f.root, core.ProvenanceDir)); !os.IsNotExist(err) {
		f.t.Fatalf("provenance off wrote %s: %v", core.ProvenanceDir, err)
	}
}

func implementerTest(f provFixture) {
	f.edit("s1", "t2", "p/x_test.xx", "check two\n", core.Implementer)
}

func TestEvaluateIndependenceScenarios(t *testing.T) {
	scenarios := []evalScenario{
		{name: "implementer source, verifier test", pr: independentPR, code: 0, ind: "pass"},
		{name: "implementer wrote source and test", code: 1, ind: "fail",
			pr: func(f provFixture) {
				f.edit("s1", "t1", "p/x.xx", "two\n", core.Implementer)
				implementerTest(f)
			},
			indHas: [][]string{{"p/x_test.xx", core.Implementer, "s1"}},
			check: func(t *testing.T, rep app.EvalReport) {
				ind, _ := entry(rep, "IND-VERIFIER-DISTINCT", "")
				if detailWith(ind, "p/x.xx:") {
					t.Errorf("implementer-authored source must not fail: %v", ind.Details)
				}
			}},
		{name: "verifier wrote source", code: 1, ind: "fail",
			pr:     func(f provFixture) { f.edit("s1", "t1", "p/x.xx", "two\n", core.Verifier) },
			indHas: [][]string{{"p/x.xx", core.Verifier}}},
		{name: "unclaimed file with unbroken main-thread chain", code: 0, ind: "pass",
			base: func(f provFixture) { f.write("README.md", "hi\n") },
			pr:   func(f provFixture) { f.edit("s1", "t1", "README.md", "hello\n", "") }},
		{name: "record from another session", code: 1, ind: "fail",
			pr:     func(f provFixture) { independentPR(f); f.copySession("s1.jsonl", "s9.jsonl") },
			indHas: [][]string{{core.ProvenanceDir + "/s9.jsonl:1"}}},
		{name: "nested provenance file", code: 1, ind: "fail",
			pr:     func(f provFixture) { independentPR(f); f.copySession("s1.jsonl", "old/s1.jsonl") },
			indHas: [][]string{{core.ProvenanceDir + "/old/s1.jsonl"}}},
		{name: "earlier record rewritten", code: 1, ind: "fail",
			base: func(f provFixture) { f.edit("s1", "t0", "p/x_test.xx", "check one, verified\n", core.Verifier) },
			pr: func(f provFixture) {
				independentPR(f)
				data, err := os.ReadFile(filepath.Join(f.root, core.ProvenanceDir, "s1.jsonl"))
				if err != nil {
					f.t.Fatal(err)
				}
				f.write(core.ProvenanceDir+"/s1.jsonl", strings.Replace(string(data), "id-"+core.Verifier, "id-rewritten", 1))
			},
			check: func(t *testing.T, rep app.EvalReport) {
				ind, _ := entry(rep, "IND-VERIFIER-DISTINCT", "")
				for _, d := range ind.Details {
					if strings.Contains(d, core.ProvenanceDir+"/s1.jsonl") && !strings.Contains(d, "gap") {
						return
					}
				}
				t.Errorf("no detail names the rewritten s1.jsonl as an append-only failure: %v", ind.Details)
			}},
		{name: "records only appended", code: 0, ind: "pass", cfg: "pass",
			base: func(f provFixture) { f.edit("s1", "t0", "p/x_test.xx", "check one, verified\n", core.Verifier) },
			pr:   independentPR},
		{name: "agent_id with two agent types", code: 1, ind: "fail",
			pr: func(f provFixture) {
				f.edit("s1", "t1", "p/x.xx", "two\n", core.Implementer)
				if err := app.WritePending(f.m, "s1", "t2", "p/x_test.xx"); err != nil {
					f.t.Fatal(err)
				}
				f.write("p/x_test.xx", "check two\n")
				if code, _, stderr := assure("record", "--session", "s1", "--tool", "Edit", "--tool-use-id", "t2",
					"--agent-type", core.Verifier, "--agent-id", "id-"+core.Implementer); code != 0 {
					f.t.Fatal(stderr)
				}
			},
			indHas: [][]string{{"id-" + core.Implementer, core.Verifier, core.Implementer}}},
		{name: "adapter listing a built-in objective", code: 1, ind: "pass", cfg: "pass",
			pr: func(f provFixture) {
				xxAdapter(f.t, `{"IND-VERIFIER-DISTINCT":{"tool":"fake"},"CFG-PROTECTED":{"tool":"fake"}}`)
				independentPR(f)
			},
			check: func(t *testing.T, rep app.EvalReport) {
				for _, id := range []string{"IND-VERIFIER-DISTINCT", "CFG-PROTECTED"} {
					if !slices.ContainsFunc(rep.Problems, func(p string) bool { return strings.Contains(p, "assure-adapter-xx") && strings.Contains(p, id) }) {
						t.Errorf("no problem names the adapter listing %s: %v", id, rep.Problems)
					}
					if e, ok := entry(rep, id, "xx"); ok {
						t.Errorf("%s ran through the adapter: %+v", id, e)
					}
				}
			}},
		{name: "approved hand edit", manifest: requiredManifest, code: 0, ind: "pass", cfg: "pass",
			pr:      func(f provFixture) { handEdit(f); f.write(".assure/waivers.yaml", xxWaivers+"# reviewed\n") },
			reviews: func(f provFixture) string { return f.approval("Reviewer", f.head()) },
			indHas:  [][]string{{"p/x.xx", "review"}},
			cfgHas:  [][]string{{".assure/waivers.yaml", "review"}},
			summary: [][]string{{"p/x.xx", "review"}, {".assure/waivers.yaml", "review"}}},
		{name: "approval does not excuse a role violation", code: 1, ind: "fail", pr: implementerTest,
			reviews: func(f provFixture) string { return f.approval("reviewer", f.head()) },
			indHas:  [][]string{{"p/x_test.xx", core.Implementer}}},
		{name: "author approves own PR", manifest: requiredManifest, code: 1, ind: "fail", pr: handEdit,
			reviews: func(f provFixture) string { return f.approval("OWNER", f.head()) }},
		{name: "approval on an older commit", manifest: requiredManifest, code: 1, ind: "fail", pr: handEdit,
			reviews: func(f provFixture) string {
				out, err := exec.CommandContext(f.t.Context(), "git", "-C", f.root, "rev-parse", "main").Output()
				if err != nil {
					f.t.Fatal(err)
				}
				return f.approval("reviewer", strings.TrimSpace(string(out)))
			}},
		{name: "turning review off is judged under required", manifest: requiredManifest, code: 1, ind: "fail", cfg: "fail",
			pr:     adoptManifest,
			indHas: [][]string{{"p/x.xx", "gap"}},
			cfgHas: [][]string{{"assurance.yaml"}}},
		{name: "first adoption is judged under optional without provenance", code: 0, ind: absent, cfg: "pass",
			base: func(f provFixture) {
				if err := os.Remove(filepath.Join(f.root, "assurance.yaml")); err != nil {
					f.t.Fatal(err)
				}
			},
			pr:      adoptManifest,
			cfgHas:  [][]string{{"assurance.yaml", "unreviewed", "human_review: optional"}},
			summary: [][]string{{"assurance.yaml", "unreviewed"}}},
		{name: "an unparseable base manifest is judged under optional without provenance", code: 0, ind: absent, cfg: "pass",
			base:   func(f provFixture) { f.write("assurance.yaml", "provenance: true\nhuman_review: {B: required\n") },
			pr:     adoptManifest,
			cfgHas: [][]string{{"assurance.yaml", "unreviewed", "human_review: optional"}}},
		{name: "provenance off at base records nothing and is not judged", manifest: xxManifestOff, code: 0, ind: absent, cfg: "pass",
			pr: unrecordedPR},
		{name: "turning provenance on is not judged until the next change", manifest: xxManifestOff, code: 0, ind: absent,
			pr: func(f provFixture) { f.write("assurance.yaml", xxManifest); handEdit(f) }},
		{name: "turning provenance off is still judged", code: 0, ind: "pass",
			pr:     func(f provFixture) { f.write("assurance.yaml", xxManifestOff); handEdit(f) },
			indHas: [][]string{{"p/x.xx", "unreviewed", "human_review: optional"}}},
		{name: "optional review lists unreviewed paths", code: 0, ind: "pass", cfg: "pass",
			pr:      func(f provFixture) { handEdit(f); f.write(".assure/waivers.yaml", xxWaivers+"# unreviewed\n") },
			indHas:  [][]string{{"p/x.xx", "unreviewed", "human_review: optional"}},
			cfgHas:  [][]string{{".assure/waivers.yaml", "unreviewed", "human_review: optional"}},
			summary: [][]string{{"p/x.xx", "unreviewed"}, {".assure/waivers.yaml", "unreviewed"}}},
		{name: "optional review does not excuse a role violation", manifest: optionalManifest, code: 1, ind: "fail",
			pr: implementerTest, indHas: [][]string{{"p/x_test.xx", core.Implementer}}},
		{name: "gap at an optional level does not hide the author", manifest: optionalManifest, code: 1, ind: "fail",
			pr:     func(f provFixture) { implementerTest(f); f.write("p/x_test.xx", "check three, by hand\n") },
			indHas: [][]string{{"p/x_test.xx", core.Implementer}}},
	}
	for _, s := range scenarios {
		t.Run(s.name, s.run)
	}
}

func TestEvaluateReviewsFileErrorsExitTwo(t *testing.T) {
	f := provBase(t, xxManifest, nil)
	handEdit(f)
	f.commit()
	stale := f.reviews("stale.json", `{"author":"owner","head":"`+strings.Repeat("a", 40)+`","reviews":[]}`)
	if code, _, stderr, _ := f.evaluate("--reviews", stale); code != 2 || !strings.Contains(stderr, "stale.json") {
		t.Fatalf("stale head: exit %d %q", code, stderr)
	}
	noReport(t, f.repo)
	bad := f.reviews("bad-state.json", fmt.Sprintf(`{"author":"owner","head":%q,"reviews":[{"login":"r","state":"LGTM","commit":%q}]}`, f.head(), f.head()))
	if code, _, stderr, _ := f.evaluate("--reviews", bad); code != 2 || !strings.Contains(stderr, "bad-state.json") || !strings.Contains(stderr, "/reviews/0/state") {
		t.Fatalf("unknown state: exit %d %q", code, stderr)
	}
	noReport(t, f.repo)
	if code, _, stderr, _ := f.evaluate("--reviews", filepath.Join(f.root, "absent.json")); code != 2 {
		t.Fatalf("missing reviews file: exit %d %q", code, stderr)
	}
}

func TestEvaluateWithProvenanceAndReviewsIsReproducible(t *testing.T) {
	f := provBase(t, optionalManifest, nil)
	independentPR(f)
	f.write("README.md", "by hand\n")
	f.commit()
	reviews := f.approval("reviewer", f.head())
	var runs [2][]byte
	var outs [2]string
	for i := range runs {
		code, stdout, stderr := assure("evaluate", "--changed-from", "main", "--date", "2026-10-01", "--reviews", reviews)
		if code == 2 {
			t.Fatalf("run %d: %s", i, stderr)
		}
		_, runs[i] = readReport(t, f.repo)
		outs[i] = stdout
	}
	if !bytes.Equal(runs[0], runs[1]) || outs[0] != outs[1] {
		t.Fatalf("reruns differ:\n%s\n---\n%s", runs[0], runs[1])
	}
}
