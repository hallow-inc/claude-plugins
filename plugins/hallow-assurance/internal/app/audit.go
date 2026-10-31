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

func Audit(m *core.Manifest, scope []ScopeFile) ([]AuditOutcome, error) {
	byLang := map[string][]ScopeFile{}
	for _, f := range scope {
		byLang[f.Lang] = append(byLang[f.Lang], f)
	}
	outRoot := filepath.Join(m.Root, AuditDir)
	if err := os.RemoveAll(outRoot); err != nil {
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
	results := make([][]AuditOutcome, len(langs))
	var wg sync.WaitGroup
	for i, lang := range langs {
		if err := errs[lang]; err != nil {
			results[i] = []AuditOutcome{{Lang: lang, Objective: "-", Status: auditNotAudited, Reason: err.Error()}}
			continue
		}
		jobs, missing := planAudit(m, byID, lang, descs[lang], byLang[lang], filepath.Join(outRoot, lang), scopeFiles[lang])
		wg.Go(func() {
			results[i] = auditLanguage(m, lang, jobs, missing)
		})
	}
	wg.Wait()
	out := slices.Concat(results...)
	slices.SortFunc(out, func(a, b AuditOutcome) int {
		return cmp.Or(cmp.Compare(a.Lang, b.Lang), cmp.Compare(a.Objective, b.Objective))
	})
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

func auditLanguage(m *core.Manifest, lang string, jobs []job, reasons []string) []AuditOutcome {
	var out []AuditOutcome
	var mu sync.Mutex
	record := func(j job, ev core.Evidence, run adapterproto.Run) {
		mu.Lock()
		defer mu.Unlock()
		if run.Environment != nil {
			reasons = append(reasons, run.Environment.Message)
		} else if len(ev.Errs) > 0 {
			reasons = append(reasons, ev.Errs[0].Error())
		} else {
			out = append(out, AuditOutcome{Lang: lang, Objective: j.obj.ID, Status: auditRan})
		}
	}
	rest := jobs
	skipMutation := skipTestsUnknown
	i := slices.IndexFunc(jobs, func(j job) bool { return j.obj.ID == auditTests })
	if i >= 0 {
		rest = slices.Delete(slices.Clone(jobs), i, i+1)
		ev, run := collect(m, jobs[i], "", EvaluateTimeout)
		record(jobs[i], ev, run)
		skipMutation = suiteState(ev, run)
	}
	var wg sync.WaitGroup
	for _, j := range rest {
		if j.obj.ID == auditMutation && skipMutation != "" {
			mu.Lock()
			out = append(out, AuditOutcome{Lang: lang, Objective: j.obj.ID, Status: auditSkipped, Reason: skipMutation})
			mu.Unlock()
			continue
		}
		wg.Go(func() {
			ev, run := collect(m, j, "", EvaluateTimeout)
			record(j, ev, run)
		})
	}
	wg.Wait()
	if len(reasons) > 0 {
		out = append(out, AuditOutcome{Lang: lang, Objective: "-", Status: auditNotAudited, Reason: strings.Join(reasons, "; ")})
	}
	return out
}

func suiteState(ev core.Evidence, run adapterproto.Run) string {
	if run.Environment != nil || len(ev.Errs) > 0 {
		return skipTestsUnknown
	}
	if len(ev.Failing) > 0 {
		return skipTestsFailing
	}
	if len(ev.Problems) > 0 {
		return skipTestsUnknown
	}
	return ""
}
