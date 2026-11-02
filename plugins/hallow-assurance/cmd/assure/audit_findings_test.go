package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type cliFindings struct {
	Commit   string   `json:"commit"`
	Dirty    bool     `json:"dirty"`
	Scope    []string `json:"scope"`
	Findings []struct {
		Kind     string `json:"kind"`
		Language string `json:"language"`
		Detail   string `json:"detail"`
	} `json:"findings"`
	Unaudited []cliUnaudited `json:"unaudited"`
}

type cliUnaudited struct {
	Language string `json:"language"`
	Message  string `json:"message"`
}

func (f auditPlanFixture) readFindingsFile(t fataler, where string) cliFindings {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, auditStateDir, "findings.json"))
	if err != nil {
		t.Fatalf("assure audit wrote no findings.json: %v (%s); the findings file is what the skill and destinations read", err, where)
	}
	if vs, err := schemas.Validate(schemas.AuditFindings, data); err != nil || len(vs) > 0 {
		t.Fatalf("findings.json violates %s: %v %v (%s)\n%s", schemas.AuditFindings, vs, err, where, data)
	}
	var out cliFindings
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%v", err)
	}
	if head := gitIn(t, f.root, "rev-parse", "HEAD"); out.Commit != head {
		t.Fatalf("commit %q, want HEAD %q (%s)", out.Commit, head, where)
	}
	if dirty := gitIn(t, f.root, "status", "--porcelain", "--", ".", ":(exclude).assure/state") != ""; out.Dirty != dirty {
		t.Fatalf("dirty %v, want %v (%s); a dirty tree means the commit alone does not reproduce the findings", out.Dirty, dirty, where)
	}
	return out
}

func (f auditPlanFixture) checkFindingsFile(t fataler, args []string, red map[string]bool, where string) {
	t.Helper()
	got := f.readFindingsFile(t, where)
	if !slices.Equal(got.Scope, args) {
		t.Fatalf("scope %q, want the command's arguments verbatim %q (%s)", got.Scope, args, where)
	}
	for _, lang := range auditLangs {
		n := 0
		for _, g := range got.Findings {
			if g.Language != lang {
				continue
			}
			switch g.Kind {
			case "tests-failing":
				n++
				if !strings.Contains(g.Detail, "TestBroken") {
					t.Fatalf("tests-failing detail %q does not name the failing test TestBroken (%s)", g.Detail, where)
				}
			case "surviving-mutant":
				t.Fatalf("%s has a surviving-mutant finding %+v though no adapter reported a mutant (%s)", lang, g, where)
			}
		}
		if want := map[bool]int{true: 1}[red[lang]]; n != want {
			t.Fatalf("%s has %d tests-failing findings, want %d (%s); a red suite is reported once per language, and only a suite that ran and failed is red", lang, n, want, where)
		}
	}
}

func okEvidence() map[string][2]string {
	run := func(typ string) string {
		return `{"protocol":2,"evidence":[{"type":"` + typ + `","path":"ev"}],"tool_versions":{"t":"1"},"pinned_by":{}}`
	}
	var lcov, fuzz strings.Builder
	fuzz.WriteString("<testsuites>")
	for _, p := range auditFiles {
		lcov.WriteString("SF:" + p + "\nend_of_record\n")
		fuzz.WriteString(`<testsuite name="` + p + `"><properties><property name="assure.source" value="` + p + `"/></properties><testcase name="FuzzOK"/></testsuite>`)
	}
	fuzz.WriteString("</testsuites>")
	return map[string][2]string{
		objMutation: {run("mutation.report"), `{"schemaVersion":"1","thresholds":{"high":80,"low":60},"files":{}}`},
		objCoverage: {run("coverage.resolution"), lcov.String()},
		objFuzz:     {run("fuzz.run"), fuzz.String()},
	}
}

func adapterLeadProblem(lang, msg string) string {
	exe := "assure-adapter-" + lang
	rest, ok := strings.CutPrefix(msg, exe)
	switch {
	case !ok || (!strings.HasPrefix(rest, ": ") && !strings.HasPrefix(rest, " ")):
		return `does not start with ` + exe + ` followed by ": " or " "`
	case strings.HasPrefix(rest, ": "+exe):
		return "names " + exe + " twice at its start"
	}
	return ""
}

func (f auditPlanFixture) checkUnauditedNames(t fataler, lang string, parts []string, where string) {
	t.Helper()
	got := f.readFindingsFile(t, where)
	i := slices.IndexFunc(got.Unaudited, func(u cliUnaudited) bool { return u.Language == lang })
	if i >= 0 {
		if p := adapterLeadProblem(lang, got.Unaudited[i].Message); p != "" {
			t.Fatalf("findings.json unaudited message %q for %s %s (%s); the adapter's name, said once, tells the user which tool to fix", got.Unaudited[i].Message, lang, p, where)
		}
	}
	for _, part := range parts {
		if i < 0 || !strings.Contains(got.Unaudited[i].Message, part) {
			t.Fatalf("findings.json unaudited %+v, want an entry for %s containing %q (%s); a language whose tool failed was not audited, and the findings file must not read as clean", got.Unaudited, lang, part, where)
		}
	}
}
