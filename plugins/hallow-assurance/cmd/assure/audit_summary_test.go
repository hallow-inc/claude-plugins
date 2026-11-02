package main

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

const (
	summaryOK          = "ok"
	summaryRed         = "red"
	summaryEnvironment = "environment"
	summaryDescribe    = "describe fails"
	summaryCrash       = "suite crashes"
	describeBroke      = "describe-broke"
	suiteCrashed       = "suite-runner-crashed"
)

var (
	summaryStates  = []string{summaryOK, summaryRed, summaryEnvironment, summaryDescribe, summaryCrash}
	summarySources = map[string]string{"xx": "p/a.xx", "yy": "p/c.yy"}
	summaryCauses  = map[string]string{summaryEnvironment: envMessage, summaryDescribe: describeBroke, summaryCrash: suiteCrashed}
)

func strykerReport(path string, survivors []int) string {
	ms := make([]string, len(survivors))
	for i, l := range survivors {
		ms[i] = fmt.Sprintf(`{"id":"m%d","mutatorName":"M","status":"Survived","location":{"start":{"line":%d,"column":1},"end":{"line":%d,"column":1}}}`, i, l, l)
	}
	return `{"schemaVersion":"1","thresholds":{"high":80,"low":60},"files":{"` + path + `":{"language":"x","source":"","mutants":[` + strings.Join(ms, ",") + `]}}}`
}

func lcovUncovered(path string, lines []int) string {
	var b strings.Builder
	for _, p := range auditFiles {
		b.WriteString("SF:" + p + "\n")
		if p == path {
			for _, l := range lines {
				fmt.Fprintf(&b, "DA:%d,0\n", l)
			}
		}
		b.WriteString("end_of_record\n")
	}
	return b.String()
}

func (f auditPlanFixture) breakDescribe(t fataler, lang string) {
	p := filepath.Join(f.bin, "assure-adapter-"+lang)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v", err)
	}
	script := strings.Replace(string(data), "describe) printf", "describe) echo "+describeBroke+" >&2; exit 4 ;;\nunused-describe) printf", 1)
	if script == string(data) {
		t.Fatalf("fake adapter script has no describe branch to break")
	}
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
}

func summaryResponses(t *rapid.T, lang, state string, survivors, uncovered []int) map[string]string {
	ok := okEvidence()
	resp := map[string]string{
		objTests + ".json":    testsRun,
		objTests + ".ev":      greenJUnit,
		objMutation + ".json": ok[objMutation][0],
		objMutation + ".ev":   strykerReport(summarySources[lang], survivors),
		objCoverage + ".json": ok[objCoverage][0],
		objCoverage + ".ev":   lcovUncovered(summarySources[lang], uncovered),
	}
	switch state {
	case summaryRed:
		resp[objTests+".ev"] = redJUnit
	case summaryEnvironment:
		obj := rapid.SampledFrom(auditable).Draw(t, lang+" environment objective")
		resp[obj+".json"] = auditEnvRun
		delete(resp, obj+".ev")
	case summaryCrash:
		delete(resp, objTests+".json")
		delete(resp, objTests+".ev")
		resp[objTests+".fail"] = suiteCrashed
	}
	return resp
}

func wantCountLine(fs cliFindings, path string) string {
	if len(fs.Findings) == 0 {
		return "audit: 0 findings in " + path
	}
	byKind := map[string]int{}
	for _, g := range fs.Findings {
		byKind[g.Kind]++
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(byKind)) {
		parts = append(parts, fmt.Sprintf("%d %s", byKind[k], k))
	}
	noun := "findings"
	if len(fs.Findings) == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("audit: %d %s (%s) in %s", len(fs.Findings), noun, strings.Join(parts, ", "), path)
}

func countKind(fs cliFindings, lang, kind string) int {
	n := 0
	for _, g := range fs.Findings {
		if g.Language == lang && g.Kind == kind {
			n++
		}
	}
	return n
}

func TestAuditExitAndSummaryReportFindingsAndEveryLanguageNotAudited(t *testing.T) {
	f := newAuditPlanFixture(t)
	writeOrFail(t, filepath.Join(f.root, "assurance.yaml"), "version: 0\ncatalog: v0\nlanguages: [xx, yy]\ndefault_level: A\ncomponents:\n  - {path: '**', level: A, inputs: true}\n")
	findingsPath := filepath.Join(f.root, auditStateDir, "findings.json")
	reportPath := filepath.Join(f.root, filepath.FromSlash(app.ReportFile))
	rapid.Check(t, func(t *rapid.T) {
		states := map[string]string{}
		survivors := map[string][]int{}
		uncovered := map[string][]int{}
		var broken []string
		for _, lang := range auditLangs {
			states[lang] = rapid.SampledFrom(summaryStates).Draw(t, lang+" state")
			lines := rapid.IntRange(1, auditFileLen)
			survivors[lang] = rapid.SliceOfNDistinct(lines, 0, 3, rapid.ID[int]).Draw(t, lang+" surviving mutant lines")
			uncovered[lang] = rapid.SliceOfNDistinct(lines, 0, 3, rapid.ID[int]).Draw(t, lang+" uncovered lines")
			f.install(t, lang, auditable, summaryResponses(t, lang, states[lang], survivors[lang], uncovered[lang]))
			if states[lang] == summaryDescribe {
				f.breakDescribe(t, lang)
			}
			if summaryCauses[states[lang]] != "" {
				broken = append(broken, lang)
			}
		}
		where := fmt.Sprintf("states %v, surviving mutants %v, uncovered %v", states, survivors, uncovered)

		code, out, stderr := assure("audit", ".")

		if want := map[bool]int{true: 1}[len(broken) > 0]; code != want {
			t.Fatalf("exit %d, want %d (%s); findings never fail an audit, but a language that could not be audited must, so a missing tool is never read as a clean run\nstdout:\n%s\nstderr:\n%s", code, want, where, out, stderr)
		}
		if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
			t.Fatalf("audit left %s (stat error %v) (%s); audit is advisory and must never write the evaluation report a gate reads", app.ReportFile, err, where)
		}
		fs := f.readFindingsFile(t, where)
		checkAuditedLanguageFindings(t, fs, states, survivors, uncovered, where)
		var unaudited []string
		for _, u := range fs.Unaudited {
			unaudited = append(unaudited, u.Language)
		}
		if !slices.Equal(slices.Sorted(slices.Values(unaudited)), broken) {
			t.Fatalf("findings.json lists %q as not audited, want %q (%s)", unaudited, broken, where)
		}

		s := parseAuditStdout(t, out)
		if want := wantCountLine(fs, findingsPath); s.count != want {
			t.Fatalf("count line %q, want %q (%s); the summary counts findings.json by kind, kinds in name order, and names where the file is\nstdout:\n%s", s.count, want, where, out)
		}
		wantNotAudited := checkUnauditedMessages(t, fs, states, s.outcomes, where)
		if !slices.Equal(s.notAudited, wantNotAudited) {
			t.Fatalf("not-audited summary lines %q, want %q (%s); one line per language not audited, by language, carrying the message findings.json records\nstdout:\n%s", s.notAudited, wantNotAudited, where, out)
		}
		stderrLines := strings.Split(stderr, "\n")
		for _, l := range wantNotAudited {
			if !slices.Contains(stderrLines, l) && !slices.Contains(stderrLines, "assure: "+l) {
				t.Fatalf("stderr lacks %q (%s); a not-audited language must reach stderr too, where a scheduled run's log shows it\nstderr:\n%s", l, where, stderr)
			}
		}
	})
}

func checkAuditedLanguageFindings(t *rapid.T, fs cliFindings, states map[string]string, survivors, uncovered map[string][]int, where string) {
	for _, lang := range auditLangs {
		var want map[string]int
		switch states[lang] {
		case summaryOK:
			want = map[string]int{"surviving-mutant": len(survivors[lang]), "uncovered": len(uncovered[lang]), "tests-failing": 0}
		case summaryRed:
			want = map[string]int{"surviving-mutant": 0, "uncovered": len(uncovered[lang]), "tests-failing": 1}
		}
		for kind, n := range want {
			if got := countKind(fs, lang, kind); got != n {
				t.Fatalf("findings.json has %d %s findings for %s, want %d (%s); a language that ran keeps its findings even when another language could not be audited", got, kind, lang, n, where)
			}
		}
	}
}

func checkUnauditedMessages(t *rapid.T, fs cliFindings, states, outcomes map[string]string, where string) []string {
	us := slices.Clone(fs.Unaudited)
	slices.SortFunc(us, func(a, b cliUnaudited) int { return strings.Compare(a.Language, b.Language) })
	lines := make([]string, len(us))
	for i, u := range us {
		if p := adapterLeadProblem(u.Language, u.Message); p != "" {
			t.Fatalf("findings.json not-audited message %q for %s %s (%s); the adapter's name, said once, tells the user which tool to fix", u.Message, u.Language, p, where)
		}
		if !strings.Contains(u.Message, summaryCauses[states[u.Language]]) {
			t.Fatalf("findings.json not-audited message %q for %s does not contain the cause %q (%s)", u.Message, u.Language, summaryCauses[states[u.Language]], where)
		}
		reason, ok := strings.CutPrefix(outcomes[u.Language+"\t-"], "not-audited\t")
		if p := adapterLeadProblem(u.Language, reason); !ok || p != "" {
			t.Fatalf("not-audited outcome line for %s reads %q; its reason %s (%s)", u.Language, outcomes[u.Language+"\t-"], cmp.Or(p, "is missing"), where)
		}
		lines[i] = "audit: " + u.Language + " not audited: " + strings.Join(strings.Fields(u.Message), " ")
	}
	return lines
}
