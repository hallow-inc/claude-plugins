package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const ReportFile = ".assure/state/report.json"

type ChangedFrom struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type Entry struct {
	Objective      string            `json:"objective"`
	Language       string            `json:"language,omitempty"`
	Status         core.Status       `json:"status"`
	Details        []string          `json:"details"`
	Waivers        []core.Waiver     `json:"waivers"`
	Baselined      int               `json:"baselined"`
	UnderThreshold int               `json:"under_threshold"`
	Resolutions    []core.Resolution `json:"resolutions,omitempty"`
}

type RemovableEntry struct {
	Entry  core.BaselineEntry `json:"entry"`
	Unused int                `json:"unused"`
}

type EvalReport struct {
	Version              int                          `json:"version"`
	Commit               string                       `json:"commit"`
	ChangedFrom          ChangedFrom                  `json:"changed_from"`
	Date                 string                       `json:"date"`
	Catalog              string                       `json:"catalog"`
	ChangedFiles         int                          `json:"changed_files"`
	ToolVersions         map[string]map[string]string `json:"tool_versions"`
	Problems             []string                     `json:"problems"`
	ExpiredWaivers       []core.Waiver                `json:"expired_waivers"`
	RemovableBaseline    []RemovableEntry             `json:"removable_baseline"`
	RemovableResolutions []core.Resolution            `json:"removable_resolutions"`
	Objectives           []Entry                      `json:"objectives"`
}

func (r EvalReport) Blocking() bool {
	return len(r.Problems) > 0 || len(r.ExpiredWaivers) > 0 ||
		slices.ContainsFunc(r.Objectives, func(e Entry) bool { return e.Status == core.Fail })
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (r EvalReport) Marshal() ([]byte, error) {
	r.Problems = orEmpty(r.Problems)
	r.ExpiredWaivers = orEmpty(r.ExpiredWaivers)
	r.RemovableBaseline = orEmpty(r.RemovableBaseline)
	r.RemovableResolutions = orEmpty(r.RemovableResolutions)
	r.Objectives = slices.Clone(orEmpty(r.Objectives))
	for i := range r.Objectives {
		r.Objectives[i].Details = orEmpty(r.Objectives[i].Details)
		r.Objectives[i].Waivers = orEmpty(r.Objectives[i].Waivers)
	}
	if r.ToolVersions == nil {
		r.ToolVersions = map[string]map[string]string{}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	vs, err := schemas.Validate(schemas.Report, data)
	if err == nil && len(vs) > 0 {
		msgs := make([]string, len(vs))
		for i, v := range vs {
			msgs[i] = v.String()
		}
		err = errors.New(strings.Join(msgs, "; "))
	}
	if err != nil {
		return nil, fmt.Errorf("report violates report.schema.json: %w", err)
	}
	return data, nil
}

func revParse(root, ref string) (string, error) {
	out, err := git(root, "rev-parse", "--verify", ref+"^{}")
	if err != nil {
		return "", err
	}
	return out[0], nil
}

type evalResult struct {
	entry Entry
	ev    core.Evidence
	tools map[string]string
	used  map[core.Fingerprint]int
}

func Evaluate(m *core.Manifest, ref, date string) (EvalReport, error) {
	ws, err := core.LoadWaivers(m.Root)
	if err != nil {
		return EvalReport{}, err
	}
	bl, err := core.LoadBaseline(m.Root)
	if err != nil {
		return EvalReport{}, err
	}
	head, err := revParse(m.Root, "HEAD")
	if err != nil {
		return EvalReport{}, err
	}
	base, err := revParse(m.Root, ref)
	if err != nil {
		return EvalReport{}, err
	}
	rep := EvalReport{
		Commit:         head,
		ChangedFrom:    ChangedFrom{Ref: ref, SHA: base},
		Date:           date,
		Catalog:        m.Catalog.Version,
		ToolVersions:   map[string]map[string]string{},
		ExpiredWaivers: core.Expired(ws, date),
	}
	slices.SortFunc(rep.ExpiredWaivers, func(a, b core.Waiver) int {
		return cmp.Or(cmp.Compare(a.Objective, b.Objective), cmp.Compare(a.Scope, b.Scope), cmp.Compare(a.Expires, b.Expires))
	})
	changed, err := ChangedFiles(m.Root, ref)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return rep, nil
	}
	rep.ChangedFiles = len(changed)
	if len(changed) == 0 {
		return rep, nil
	}
	fix, err := FixCommit(m.Root, ref)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
	}
	jobs, rest := planEvaluate(m, changed, fix, &rep)
	if err := annotate(m.Root, ref, jobs); err != nil {
		rep.Problems = append(rep.Problems, err.Error())
	}
	results := make([]evalResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Go(func() {
			ev, tools := collect(m, j, ref, EvaluateTimeout)
			out := core.DecideObjective(j.obj, j.files, ev, ws, bl.For(j.obj.ID), date)
			results[i] = evalResult{entry: entryOf(j.lang, out), ev: ev, tools: tools, used: out.BaselineUsed}
		})
	}
	wg.Wait()
	used, ran := rep.fold(jobs, results)
	all := allChanged(m, changed, fix)
	for _, o := range rest {
		ev := core.Evidence{Problems: []string{"no adapter lists " + o.ID}}
		rep.Objectives = append(rep.Objectives, entryOf("", core.DecideObjective(o, all, ev, ws, bl.For(o.ID), date)))
	}
	slices.SortFunc(rep.Objectives, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Objective, b.Objective), cmp.Compare(a.Language, b.Language))
	})
	for _, r := range bl.Removable(used, func(e core.BaselineEntry) bool { return ran[e.Objective][e.Path] }) {
		rep.RemovableBaseline = append(rep.RemovableBaseline, RemovableEntry{Entry: r.Entry, Unused: r.Unused})
	}
	rep.RemovableResolutions = removableResolutions(m.Root, results, &rep)
	return rep, nil
}

func (rep *EvalReport) fold(jobs []job, results []evalResult) (used map[core.Fingerprint]int, ran map[string]map[string]bool) {
	used = map[core.Fingerprint]int{}
	ran = map[string]map[string]bool{}
	for i, res := range results {
		rep.Objectives = append(rep.Objectives, res.entry)
		if len(res.tools) > 0 {
			if rep.ToolVersions[jobs[i].lang] == nil {
				rep.ToolVersions[jobs[i].lang] = map[string]string{}
			}
			maps.Copy(rep.ToolVersions[jobs[i].lang], res.tools)
		}
		for fp, n := range res.used {
			used[fp] += n
		}
		if len(res.ev.Problems) > 0 {
			continue
		}
		if ran[jobs[i].obj.ID] == nil {
			ran[jobs[i].obj.ID] = map[string]bool{}
		}
		for _, f := range jobs[i].files {
			ran[jobs[i].obj.ID][f.Path] = true
		}
	}
	return used, ran
}

func entryOf(lang string, o core.Outcome) Entry {
	return Entry{Objective: o.ID, Language: lang, Status: o.Status, Details: o.Details, Waivers: o.Waivers, Baselined: o.Baselined, UnderThreshold: o.UnderThreshold, Resolutions: o.Resolutions}
}

func allChanged(m *core.Manifest, changed []string, fix bool) []core.ChangedFile {
	out := make([]core.ChangedFile, len(changed))
	for i, p := range changed {
		out[i] = m.ChangedFile(p)
		out[i].Fix = fix
	}
	return out
}

func FixCommit(root, ref string) (bool, error) {
	values, err := git(root, "log", "--format=%(trailers:key=Assure-Kind,valueonly)", ref+"..HEAD")
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(values, func(v string) bool { return strings.TrimSpace(v) == "fix" }), nil
}

func planEvaluate(m *core.Manifest, changed []string, fix bool, rep *EvalReport) (jobs []job, unlisted []core.Objective) {
	descs, errs := adapterproto.Descriptions(m.Root, m.Languages)
	for _, lang := range m.Languages {
		if err := errs[lang]; err != nil {
			rep.Problems = append(rep.Problems, err.Error())
		}
	}
	patterns := map[string]core.RolePatterns{}
	for lang, d := range descs {
		patterns[lang] = core.RolePatterns{Claims: d.Claims, Patterns: d.Patterns}
	}
	roles, err := core.NewRoles(patterns)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
	}
	files := map[string][]core.ChangedFile{}
	for _, p := range changed {
		for _, lang := range roles.Claimants(p) {
			f := m.ChangedFile(p)
			f.Fix = fix
			files[lang] = append(files[lang], f)
		}
	}
	byID := map[string]core.Objective{}
	for _, o := range m.Catalog.Objectives {
		byID[o.ID] = o
	}
	outRoot := filepath.Join(m.Root, EvidenceDir, "evaluate")
	_ = os.RemoveAll(outRoot)
	listed := map[string]bool{}
	for _, lang := range m.Languages {
		for _, id := range slices.Sorted(maps.Keys(descs[lang].Objectives)) {
			listed[id] = true
			o, ok := byID[id]
			if !ok {
				rep.Problems = append(rep.Problems, fmt.Sprintf("%s lists %s, but catalog %s has no such objective", adapterproto.Executable(lang), id, m.Catalog.Version))
				continue
			}
			if !core.Applicable(o, files[lang]) {
				continue
			}
			jobs = append(jobs, job{lang: lang, obj: o, files: files[lang], out: filepath.Join(outRoot, lang, id)})
		}
	}
	for _, o := range m.Catalog.Objectives {
		if !listed[o.ID] && core.Applicable(o, allChanged(m, changed, fix)) {
			unlisted = append(unlisted, o)
		}
	}
	return jobs, unlisted
}

func annotate(root, ref string, jobs []job) error {
	lines, err := AddedLines(root, ref)
	if err != nil {
		return err
	}
	var errs []error
	roles := map[string]map[string]core.Role{}
	for i, j := range jobs {
		if roles[j.lang] == nil {
			r, err := classifyExisting(root, j.lang, j.files)
			if err != nil {
				errs = append(errs, err)
			}
			roles[j.lang] = r
		}
		files := slices.Clone(j.files)
		for k := range files {
			files[k].Lines = lines[files[k].Path]
			files[k].Role = roles[j.lang][files[k].Path]
		}
		jobs[i].files = files
	}
	return errors.Join(errs...)
}

func classifyExisting(root, lang string, fs []core.ChangedFile) (map[string]core.Role, error) {
	var paths []string
	for _, f := range fs {
		ok, err := exists(root, f.Path)
		if err != nil {
			return nil, err
		}
		if ok {
			paths = append(paths, f.Path)
		}
	}
	classified, err := adapterproto.Classify(root, lang, paths)
	if err != nil {
		return nil, err
	}
	roles := map[string]core.Role{}
	for _, c := range classified {
		roles[c.Path] = core.Role(c.Role)
	}
	return roles, nil
}

func removableResolutions(root string, results []evalResult, rep *EvalReport) []core.Resolution {
	res, err := core.LoadResolutions(root)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return nil
	}
	var out []core.Resolution
	for _, r := range results {
		if r.ev.Coverage != nil {
			out = append(out, core.RemovableResolutions(*r.ev.Coverage)...)
		}
	}
	for _, r := range res {
		ok, err := exists(root, r.Path)
		if err != nil {
			rep.Problems = append(rep.Problems, err.Error())
		}
		if !ok && err == nil {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b core.Resolution) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Function, b.Function))
	})
	return slices.Compact(out)
}
