package core

import (
	"cmp"
	"slices"
)

type Status string

const (
	Pass         Status = "pass"
	Fail         Status = "fail"
	AdvisoryFail Status = "advisory-fail"
	Waived       Status = "waived"
)

type Finding struct {
	Level     Level
	Located   bool
	Path      string
	Rule      string
	Message   string
	Metric    float64
	HasMetric bool
	Text      string
}

type Evidence struct {
	Problems []string
	Failing  []string
	Findings []Finding
}

type ChangedFile struct {
	Path   string
	Level  Level
	Formal bool
	DST    bool
}

func (m *Manifest) ChangedFile(path string) ChangedFile {
	comp, level := m.Resolve(path)
	f := ChangedFile{Path: path, Level: level}
	if comp >= 0 {
		f.Formal, f.DST = m.Components[comp].Formal, m.Components[comp].DST
	}
	return f
}

func inScope(o Objective, f ChangedFile) bool {
	switch o.AppliesTo {
	case "formal":
		return f.Formal
	case "dst":
		return f.DST
	}
	return true
}

func Applicable(o Objective, changed []ChangedFile) bool {
	req, adv, _ := changedStatus(o, changed)
	return req || adv
}

type Outcome struct {
	ID             string
	Status         Status
	Details        []string
	Waivers        []Waiver
	Baselined      int
	UnderThreshold int
	BaselineUsed   map[Fingerprint]int
}

func changedStatus(o Objective, changed []ChangedFile) (req, adv bool, applicable []string) {
	for _, f := range changed {
		if !inScope(o, f) {
			continue
		}
		switch o.Levels[f.Level] {
		case "required":
			req = true
		case "advisory":
			adv = true
		default:
			continue
		}
		applicable = append(applicable, f.Path)
	}
	return req, adv, applicable
}

func DecideFast(o Objective, changed []Level, ev Evidence) Outcome {
	files := make([]ChangedFile, len(changed))
	for i, l := range changed {
		files[i] = ChangedFile{Level: l}
	}
	return DecideObjective(o, files, ev, nil, nil, "")
}

func compareFindings(a, b Finding) int {
	return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Message, b.Message),
		cmp.Compare(a.Text, b.Text), cmp.Compare(a.Level, b.Level), cmp.Compare(a.Metric, b.Metric))
}

func compareWaivers(a, b Waiver) int {
	return cmp.Or(cmp.Compare(a.Scope, b.Scope), cmp.Compare(a.Expires, b.Expires), cmp.Compare(a.Approver, b.Approver), cmp.Compare(a.Rationale, b.Rationale))
}

type decision struct {
	applied  []Waiver
	blocking bool
	advisory bool
	waived   bool
	details  []string
	excused  []string
}

func (d *decision) waive(w Waiver, text string) {
	d.waived = true
	d.excused = append(d.excused, "waived: "+text)
	if !slices.ContainsFunc(d.applied, func(x Waiver) bool { return compareWaivers(x, w) == 0 }) {
		d.applied = append(d.applied, w)
	}
}

func coveringWaiver(active []Waiver, applicable []string) (Waiver, bool) {
	if len(applicable) == 0 {
		return Waiver{}, false
	}
	for _, w := range active {
		if w.covers(applicable) {
			return w, true
		}
	}
	return Waiver{}, false
}

func (o Objective) UnderThreshold(f Finding) bool {
	th, ok := o.Threshold[f.Level]
	return ok && f.HasMetric && f.Metric <= th
}

func split(o Objective, ev Evidence) (unlocated []string, located []Finding, under int) {
	unlocated = slices.Concat(ev.Problems, ev.Failing)
	findings := slices.Clone(ev.Findings)
	slices.SortFunc(findings, compareFindings)
	for _, f := range findings {
		if !f.Located {
			unlocated = append(unlocated, f.Text)
			continue
		}
		if o.UnderThreshold(f) {
			under++
			continue
		}
		located = append(located, f)
	}
	return unlocated, located, under
}

func (d *decision) unlocated(texts []string, req bool, cover Waiver, covered bool) {
	for _, text := range texts {
		if covered {
			d.waive(cover, text)
			continue
		}
		d.details = append(d.details, text)
		d.blocking = d.blocking || req
		d.advisory = true
	}
}

func (d *decision) located(o Objective, fs []Finding, req bool, active []Waiver) {
	for _, f := range fs {
		status := o.Levels[f.Level]
		if status == "" {
			continue
		}
		if i := slices.IndexFunc(active, func(w Waiver) bool { return w.Match(f.Path) }); i >= 0 {
			d.waive(active[i], f.Text)
			continue
		}
		d.details = append(d.details, f.Text)
		d.advisory = true
		d.blocking = d.blocking || (status == "required" && req)
	}
}

func (d *decision) status() Status {
	switch {
	case d.blocking:
		return Fail
	case d.advisory:
		return AdvisoryFail
	case d.waived:
		return Waived
	}
	return Pass
}

func DecideObjective(o Objective, changed []ChangedFile, ev Evidence, ws []Waiver, baseline map[Fingerprint]int, date string) Outcome {
	req, adv, applicable := changedStatus(o, changed)
	active := activeFor(ws, o.ID, date)
	slices.SortFunc(active, compareWaivers)
	cover, covered := coveringWaiver(active, applicable)
	unlocated, located, under := split(o, ev)
	kept, used := matchBaseline(baseline, o.ID, located)
	out := Outcome{ID: o.ID, UnderThreshold: under, BaselineUsed: used}
	for _, n := range used {
		out.Baselined += n
	}
	d := &decision{}
	if req || adv {
		d.unlocated(unlocated, req, cover, covered)
	}
	d.located(o, kept, req, active)
	out.Details = append(d.details, d.excused...)
	out.Waivers = d.applied
	out.Status = d.status()
	return out
}
