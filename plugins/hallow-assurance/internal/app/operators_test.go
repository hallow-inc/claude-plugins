package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var operatorClasses = []string{"AOR", "ROR", "LCR", "ABS", "UOI"}

const operatorAdapter = `#!/bin/sh
case "$1" in
describe) printf '%s' 'DESCRIBE' ;;
run)
  echo "$2" >> 'RUNLOG'
  out=""
  while [ $# -gt 0 ]; do [ "$1" = "--out" ] && out="$2"; shift; done
  mkdir -p "$out"
  printf '%s' '<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>' > "$out/junit.xml"
  printf '%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"t":"1"},"pinned_by":{}}' ;;
*) exit 1 ;;
esac
`

type operatorRepo struct {
	root, adapter, runs string
}

func newOperatorRepo(t *testing.T) operatorRepo {
	t.Helper()
	bin, root := untrackedPinRepo(t)
	r := operatorRepo{root: root, adapter: filepath.Join(bin, "assure-adapter-xx"), runs: filepath.Join(bin, "runs.log")}
	putFile(t, root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n")
	gitIn(t, root, "add", "assurance.yaml")
	gitIn(t, root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "manifest")
	putFile(t, root, "a.xx", "changed\n")
	return r
}

func (r operatorRepo) setDescribe(t fataler, id string, entry map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"protocol": 2, "languages": []string{"xx"}, "claims": []string{"**/*.xx"},
		"patterns": map[string]any{"test": []string{"**/*_test.xx"}}, "objectives": map[string]any{id: entry},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	script := strings.NewReplacer("DESCRIBE", string(data), "RUNLOG", r.runs).Replace(operatorAdapter)
	if err := os.WriteFile(r.adapter, []byte(script), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(r.runs, nil, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

func (r operatorRepo) ran(t fataler) []string {
	t.Helper()
	data, err := os.ReadFile(r.runs)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return strings.Fields(string(data))
}

func entryFor(rep EvalReport, id string) (Entry, bool) {
	i := slices.IndexFunc(rep.Objectives, func(e Entry) bool { return e.Objective == id && e.Language == "xx" })
	if i < 0 {
		return Entry{}, false
	}
	return rep.Objectives[i], true
}

func missingClassDetail(details []string, id string, missing []string) (string, bool) {
	for _, d := range details {
		if !strings.Contains(d, "assure-adapter-xx") || !strings.Contains(d, id) {
			continue
		}
		if !slices.ContainsFunc(missing, func(c string) bool { return !strings.Contains(d, c) }) {
			return d, true
		}
	}
	return "", false
}

func TestOperatorFloorDecidesWhetherTheObjectiveRuns(t *testing.T) {
	r := newOperatorRepo(t)
	m, err := ManifestFor(r.root)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(m.Catalog.Objectives, func(o core.Objective) bool { return o.ID == "VER-TESTS-PASS" })
	if i < 0 {
		t.Fatal("VER-TESTS-PASS missing from catalog v0")
	}
	base := m.Catalog.Objectives[i]
	rapid.Check(t, func(rt *rapid.T) {
		required := rapid.SliceOfDistinct(rapid.SampledFrom(operatorClasses), rapid.ID[string]).Draw(rt, "required")
		entry := map[string]any{"tool": "t", "fast": true}
		var declared []string
		if rapid.Bool().Draw(rt, "declares") {
			declared = rapid.SliceOfDistinct(rapid.SampledFrom(operatorClasses), rapid.ID[string]).Draw(rt, "declared")
			entry["operators"] = append([]string{}, declared...)
		}
		var missing []string
		for _, c := range required {
			if !slices.Contains(declared, c) {
				missing = append(missing, c)
			}
		}
		o := base
		o.RequiredOperators = required
		m.Catalog.Objectives = []core.Objective{o}
		r.setDescribe(rt, o.ID, entry)

		rep, err := Evaluate(m, "HEAD", "2026-11-08")
		if err != nil {
			rt.Fatal(err)
		}
		e, ok := entryFor(rep, o.ID)
		if !ok {
			rt.Fatalf("evaluate has no %s entry for xx: %+v", o.ID, rep.Objectives)
		}
		checkFloorOutcome(rt, "evaluate", r.ran(rt), e.Status, e.Details, o.ID, required, declared, missing)

		r.setDescribe(rt, o.ID, entry)
		fast := FastCheck(m, "HEAD", "operators")
		j := slices.IndexFunc(fast.Results, func(res Result) bool { return res.ID == o.ID && res.Lang == "xx" })
		if j < 0 {
			rt.Fatalf("fast check has no %s result for xx: %+v", o.ID, fast.Results)
		}
		checkFloorOutcome(rt, "check", r.ran(rt), fast.Results[j].Status, fast.Results[j].Details, o.ID, required, declared, missing)
	})
}

func checkFloorOutcome(t *rapid.T, cmd string, ran []string, status core.Status, details []string, id string, required, declared, missing []string) {
	if len(missing) == 0 {
		if !slices.Equal(ran, []string{id}) {
			t.Fatalf("%s: required %v declared %v covers the floor, yet adapter runs were %v; a covering adapter must run as before", cmd, required, declared, ran)
		}
		if status != core.Pass {
			t.Fatalf("%s: required %v declared %v: %s %q; the run passed, so the objective is decided from its evidence", cmd, required, declared, status, details)
		}
		return
	}
	if len(ran) > 0 {
		t.Fatalf("%s: required %v declared %v lacks %v, yet the adapter ran %v; a tool missing a required class cannot pass, so it must not spend minutes running", cmd, required, declared, missing, ran)
	}
	if status != core.Fail {
		t.Fatalf("%s: required %v declared %v lacks %v, but status is %s; an undeclared class must fail closed", cmd, required, declared, missing, status)
	}
	d, ok := missingClassDetail(details, id, missing)
	if !ok {
		t.Fatalf("%s: no detail names assure-adapter-xx, %s, and every missing class %v: %q", cmd, id, missing, details)
	}
	for _, c := range required {
		if slices.Contains(declared, c) && strings.Contains(d, c) {
			t.Fatalf("%s: detail %q names %s, which the adapter declares; it must name only the missing classes %v", cmd, d, c, missing)
		}
	}
	at := -1
	for _, c := range missing {
		k := strings.Index(d, c)
		if k < at {
			t.Fatalf("%s: detail %q does not list missing classes in catalog order %v", cmd, d, missing)
		}
		at = k
	}
}

func TestStarterMutationObjectiveFailsClosedWithoutLCR(t *testing.T) {
	r := newOperatorRepo(t)
	m, err := ManifestFor(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, declared := range [][]string{{"AOR", "ROR"}, nil} {
		entry := map[string]any{"tool": "gremlins"}
		if declared != nil {
			entry["operators"] = declared
		}
		r.setDescribe(t, "VER-MUTATION-CHANGED", entry)
		rep, err := Evaluate(m, "HEAD", "2026-11-08")
		if err != nil {
			t.Fatal(err)
		}
		if ran := r.ran(t); len(ran) > 0 {
			t.Fatalf("declared %v: adapter ran %v; the v0 floor [AOR ROR LCR] is not covered, so the tool must not run", declared, ran)
		}
		e, ok := entryFor(rep, "VER-MUTATION-CHANGED")
		if !ok {
			t.Fatalf("declared %v: no VER-MUTATION-CHANGED entry for xx: %+v", declared, rep.Objectives)
		}
		missing := []string{"AOR", "ROR", "LCR"}[len(declared):]
		if _, ok := missingClassDetail(e.Details, "VER-MUTATION-CHANGED", missing); e.Status != core.Fail || !ok {
			t.Fatalf("declared %v: %s %q; want fail naming assure-adapter-xx, VER-MUTATION-CHANGED, and %v", declared, e.Status, e.Details, missing)
		}
	}
}
