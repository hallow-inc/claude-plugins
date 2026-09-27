package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

const (
	RunTimeout  = 150 * time.Second
	EvidenceDir = ".assure/state/evidence"
)

type Result struct {
	Lang string
	core.Outcome
}

type Report struct {
	Changed int
	Results []Result
}

func (r Report) Blocking() bool {
	for _, res := range r.Results {
		if res.Status == core.Fail {
			return true
		}
	}
	return false
}

func git(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func ChangedFiles(root, ref string) ([]string, error) {
	diff, err := git(root, "diff", "--name-only", "--relative", ref, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := git(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range append(diff, untracked...) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

type job struct {
	lang    string
	obj     core.Objective
	changed []core.Level
	out     string
}

func problem(lang, id, msg string) Result {
	return Result{Lang: lang, Outcome: core.Outcome{ID: id, Status: core.Fail, Details: []string{msg}}}
}

func FastCheck(m *core.Manifest, ref, label string) Report {
	changed, err := ChangedFiles(m.Root, ref)
	if err != nil {
		return Report{Results: []Result{problem("-", "changed-files", err.Error())}}
	}
	rep := Report{Changed: len(changed)}
	if len(changed) == 0 {
		return rep
	}
	descs, errs := adapterproto.Descriptions(m.Root, m.Languages)
	for _, lang := range m.Languages {
		if err := errs[lang]; err != nil {
			rep.Results = append(rep.Results, problem(lang, "describe", err.Error()))
		}
	}
	jobs, bad := plan(m, descs, changed, filepath.Join(m.Root, EvidenceDir, label))
	rep.Results = append(rep.Results, bad...)
	results := make([]Result, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = Result{Lang: j.lang, Outcome: core.DecideFast(j.obj, j.changed, collect(m, j, ref))}
		}()
	}
	wg.Wait()
	rep.Results = append(rep.Results, results...)
	return rep
}

func plan(m *core.Manifest, descs map[string]adapterproto.Describe, changed []string, outRoot string) ([]job, []Result) {
	patterns := map[string]core.RolePatterns{}
	for lang, d := range descs {
		patterns[lang] = core.RolePatterns{Claims: d.Claims, Patterns: d.Patterns}
	}
	roles, err := core.NewRoles(patterns)
	if err != nil {
		return nil, []Result{problem("-", "describe", err.Error())}
	}
	levels := map[string][]core.Level{}
	for _, p := range changed {
		_, level := m.Resolve(p)
		for _, lang := range roles.Claimants(p) {
			levels[lang] = append(levels[lang], level)
		}
	}
	byID := map[string]core.Objective{}
	for _, o := range m.Catalog.Objectives {
		byID[o.ID] = o
	}
	_ = os.RemoveAll(outRoot)
	var jobs []job
	var bad []Result
	for _, lang := range m.Languages {
		if len(levels[lang]) == 0 {
			continue
		}
		ids := make([]string, 0, len(descs[lang].Objectives))
		for id, o := range descs[lang].Objectives {
			if o.Fast {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			o, ok := byID[id]
			if !ok {
				bad = append(bad, problem(lang, id, fmt.Sprintf("%s marks %s fast, but catalog %s has no such objective", adapterproto.Executable(lang), id, m.Catalog.Version)))
				continue
			}
			jobs = append(jobs, job{lang: lang, obj: o, changed: levels[lang], out: filepath.Join(outRoot, lang, id)})
		}
	}
	return jobs, bad
}

func collect(m *core.Manifest, j job, ref string) core.Evidence {
	run, err := adapterproto.RunObjective(m.Root, j.lang, j.obj.ID, ref, j.out, RunTimeout)
	if err != nil {
		return core.Evidence{Problems: []string{err.Error()}}
	}
	var ev core.Evidence
	found := false
	for _, e := range run.Evidence {
		if e.Type != j.obj.Evidence {
			continue
		}
		found = true
		data, err := os.ReadFile(filepath.Join(j.out, e.Path))
		if err != nil {
			ev.Problems = append(ev.Problems, fmt.Sprintf("%s evidence: %v", e.Type, err))
			continue
		}
		readEvidence(m, e.Type, data, &ev)
	}
	if !found {
		ev.Problems = append(ev.Problems, fmt.Sprintf("%s run %s produced no %s evidence", adapterproto.Executable(j.lang), j.obj.ID, j.obj.Evidence))
	}
	return ev
}

func readEvidence(m *core.Manifest, typ string, data []byte, ev *core.Evidence) {
	switch typ {
	case "test.junit":
		j, err := evidence.ParseJUnit(data)
		if err != nil {
			ev.Problems = append(ev.Problems, err.Error())
			return
		}
		for _, name := range j.Failing {
			ev.Failing = append(ev.Failing, "failing test: "+name)
		}
	case "lint.sarif":
		results, err := evidence.ParseSARIF(data)
		if err != nil {
			ev.Problems = append(ev.Problems, err.Error())
			return
		}
		for _, r := range results {
			f := core.Finding{Text: fmt.Sprintf("%s: %s: %s", r.URI, r.RuleID, r.Message)}
			if r.URI != "" {
				_, f.Level = m.Resolve(r.URI)
				f.Located = true
			}
			ev.Findings = append(ev.Findings, f)
		}
	default:
		ev.Problems = append(ev.Problems, "the fast check cannot read "+typ+" evidence")
	}
}

const maxDetails = 20

func (r Report) Render() string {
	var b strings.Builder
	if r.Changed == 0 && len(r.Results) == 0 {
		b.WriteString("no changed files\n")
		return b.String()
	}
	for _, res := range r.Results {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", res.Status, res.ID, res.Lang)
	}
	for _, res := range r.Results {
		if res.Status == core.Pass {
			continue
		}
		fmt.Fprintf(&b, "\n%s (%s): %s\n", res.ID, res.Lang, res.Status)
		for i, d := range res.Details {
			if i == maxDetails {
				fmt.Fprintf(&b, "  … %d more\n", len(res.Details)-maxDetails)
				break
			}
			fmt.Fprintf(&b, "  - %s\n", strings.ReplaceAll(d, "\n", "\n    "))
		}
	}
	return b.String()
}
