package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const (
	AuditDir         = ".assure/state/audit"
	auditTests       = "VER-TESTS-PASS"
	auditMutation    = "VER-MUTATION-CHANGED"
	auditRan         = "ran"
	auditSkipped     = "skipped"
	auditNotAudited  = "not-audited"
	skipTestsFailing = "tests-failing"
	skipTestsUnknown = "tests-unknown"
)

var auditObjectives = []string{auditTests, auditMutation, "VER-COVERAGE-RESOLUTION", "VER-ROBUST-FUZZ"}

type AuditOutcome struct {
	Lang      string
	Objective string
	Status    string
	Reason    string
}

func Audit(m *core.Manifest, args []string, scope []ScopeFile) ([]AuditOutcome, error) {
	commit, err := revParse(m.Root, "HEAD")
	if err != nil {
		return nil, err
	}
	byLang := map[string][]ScopeFile{}
	for _, f := range scope {
		byLang[f.Lang] = append(byLang[f.Lang], f)
	}
	outRoot := filepath.Join(m.Root, AuditDir)
	if err := os.RemoveAll(outRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return nil, err
	}
	langs := slices.Sorted(maps.Keys(byLang))
	scopeFiles := map[string]string{}
	for _, lang := range langs {
		file, err := writeAuditScope(filepath.Join(outRoot, lang), byLang[lang])
		if err != nil {
			return nil, err
		}
		scopeFiles[lang] = file
	}
	descs, errs := adapterproto.Descriptions(m.Root, langs)
	byID := map[string]core.Objective{}
	for _, o := range m.Catalog.Objectives {
		byID[o.ID] = o
	}
	results := make([]*langAudit, len(langs))
	var wg sync.WaitGroup
	for i, lang := range langs {
		la := newLangAudit(m.Root, lang)
		results[i] = la
		if err := errs[lang]; err != nil {
			la.reasons = []string{err.Error()}
			continue
		}
		jobs, missing := planAudit(m, byID, lang, descs[lang], byLang[lang], filepath.Join(outRoot, lang), scopeFiles[lang])
		la.reasons = missing
		wg.Go(func() {
			la.run(m, jobs)
		})
	}
	wg.Wait()
	findings := AuditFindings{Commit: commit, Scope: args, ToolVersions: map[string]map[string]string{}}
	var out []AuditOutcome
	var drafts []draftFinding
	for _, la := range results {
		out = append(out, la.outcomes...)
		drafts = append(drafts, la.findings...)
		findings.Scores = append(findings.Scores, la.sortedScores()...)
		mergeInto(findings.ToolVersions, la.lang, la.versions)
		if len(la.reasons) > 0 {
			reason := strings.Join(la.reasons, "; ")
			out = append(out, AuditOutcome{Lang: la.lang, Objective: "-", Status: auditNotAudited, Reason: reason})
			findings.Unaudited = append(findings.Unaudited, AuditUnaudited{Language: la.lang, Message: reason})
		}
	}
	slices.SortFunc(out, func(a, b AuditOutcome) int {
		return cmp.Or(cmp.Compare(a.Lang, b.Lang), cmp.Compare(a.Objective, b.Objective))
	})
	findings.Findings = finalizeFindings(drafts)
	status, err := git(m.Root, "status", "--porcelain", "--", ".", ":(exclude).assure/state")
	if err != nil {
		return nil, err
	}
	findings.Dirty = len(status) > 0
	data, err := findings.Marshal()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outRoot, auditFindingsFile), data, 0o644); err != nil {
		return nil, err
	}
	return out, nil
}

func writeAuditScope(dir string, files []ScopeFile) (string, error) {
	s := make(adapterkit.Scope, len(files))
	for i, f := range files {
		s[i] = adapterkit.ScopeEntry{Path: f.Path, Ranges: f.Ranges}
	}
	data, _ := json.Marshal(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(dir, "scope.json")
	return file, os.WriteFile(file, data, 0o644)
}

func planAudit(m *core.Manifest, byID map[string]core.Objective, lang string, d adapterproto.Describe, files []ScopeFile, outDir, scopeFile string) (jobs []job, missing []string) {
	changed := make([]core.ChangedFile, len(files))
	for i, f := range files {
		changed[i] = m.ChangedFile(f.Path)
		changed[i].Role = core.Role(f.Role)
		for _, r := range f.Ranges {
			for l := r[0]; l <= r[1]; l++ {
				changed[i].Lines = append(changed[i].Lines, l)
			}
		}
	}
	for _, id := range auditObjectives {
		if _, listed := d.Objectives[id]; !listed {
			continue
		}
		o, ok := byID[id]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s lists %s, but catalog %s has no such objective", adapterproto.Executable(lang), id, m.Catalog.Version))
			continue
		}
		if core.Applicable(o, changed) {
			jobs = append(jobs, job{lang: lang, obj: o, files: changed, out: filepath.Join(outDir, id), scope: scopeFile})
		}
	}
	return jobs, missing
}

func (la *langAudit) run(m *core.Manifest, jobs []job) {
	rest := jobs
	skipMutation := skipTestsUnknown
	i := slices.IndexFunc(jobs, func(j job) bool { return j.obj.ID == auditTests })
	if i >= 0 {
		rest = slices.Delete(slices.Clone(jobs), i, i+1)
		ev, run := collect(m, jobs[i], "", EvaluateTimeout)
		la.record(jobs[i], ev, run)
		skipMutation = suiteState(ev, run)
	}
	var wg sync.WaitGroup
	for _, j := range rest {
		if j.obj.ID == auditMutation && skipMutation != "" {
			la.mu.Lock()
			la.outcomes = append(la.outcomes, AuditOutcome{Lang: la.lang, Objective: j.obj.ID, Status: auditSkipped, Reason: skipMutation})
			la.mu.Unlock()
			continue
		}
		wg.Go(func() {
			ev, run := collect(m, j, "", EvaluateTimeout)
			la.record(j, ev, run)
		})
	}
	wg.Wait()
}

func suiteState(ev core.Evidence, run adapterproto.Run) string {
	if run.Environment != nil || len(ev.Errs) > 0 {
		return skipTestsUnknown
	}
	if len(ev.Problems) > 0 {
		return skipTestsUnknown
	}
	if len(ev.Failing) > 0 {
		return skipTestsFailing
	}
	return ""
}
