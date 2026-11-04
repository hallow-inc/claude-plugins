package drt

import (
	"encoding/json"
	"fmt"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

type modelCase struct {
	Objective objective     `json:"objective"`
	Changed   []changedFile `json:"changed"`
	Problems  int           `json:"problems"`
	Failing   int           `json:"failing"`
	Base      *baseRun      `json:"base"`
	Mutation  bool          `json:"mutation"`
	Mutants   []mutant      `json:"mutants"`
	Fuzz      *fuzzRun      `json:"fuzz"`
	Coverage  *coverage     `json:"coverage"`
	Waivers   []waiver      `json:"waivers"`
	Date      string        `json:"date"`
}

type objective struct {
	ID        string            `json:"id"`
	Levels    map[string]string `json:"levels"`
	Threshold map[string]int    `json:"threshold"`
	AppliesTo string            `json:"applies_to"`
}

type changedFile struct {
	Path   string `json:"path"`
	Level  string `json:"level"`
	Role   string `json:"role"`
	Formal bool   `json:"formal"`
	Inputs bool   `json:"inputs"`
	Fix    bool   `json:"fix"`
	Lines  []int  `json:"lines"`
}

type baseRun struct {
	Failed      int  `json:"failed"`
	Passed      int  `json:"passed"`
	Skipped     int  `json:"skipped"`
	Errored     int  `json:"errored"`
	RequireTest bool `json:"require_test"`
}

type mutant struct {
	Path   string `json:"path"`
	Level  string `json:"level"`
	Status string `json:"status"`
}

type fuzzRun struct {
	Suites []fuzzSuite `json:"suites"`
}

type fuzzSuite struct {
	Sources []string `json:"sources"`
	Cases   []string `json:"cases"`
}

type coverage struct {
	Files []coverFile `json:"files"`
}

type coverFile struct {
	Path string   `json:"path"`
	Hits [][2]int `json:"hits"`
}

type waiver struct {
	Objective string `json:"objective"`
	Scope     string `json:"scope"`
	Expires   string `json:"expires"`
}

func names(prefix string, n int) []string {
	out := []string{}
	for i := range n {
		out = append(out, fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}

func (c modelCase) coreObjective() core.Objective {
	o := core.Objective{ID: c.Objective.ID, AppliesTo: c.Objective.AppliesTo, Levels: map[core.Level]string{}, Threshold: map[core.Level]float64{}}
	for l, s := range c.Objective.Levels {
		o.Levels[core.Level(l)] = s
	}
	for l, th := range c.Objective.Threshold {
		o.Threshold[core.Level(l)] = float64(th)
	}
	return o
}

func (c modelCase) coreChanged() []core.ChangedFile {
	var out []core.ChangedFile
	for _, f := range c.Changed {
		out = append(out, core.ChangedFile{
			Path: f.Path, Level: core.Level(f.Level), Role: core.Role(f.Role),
			Formal: f.Formal, Inputs: f.Inputs, Fix: f.Fix, Lines: f.Lines,
		})
	}
	return out
}

func (c modelCase) coreEvidence() core.Evidence {
	ev := core.Evidence{Problems: names("problem ", c.Problems), Mutation: c.Mutation}
	for _, n := range names("failing ", c.Failing) {
		ev.Failing = append(ev.Failing, core.TestFailure{Name: n, Text: n})
	}
	if b := c.Base; b != nil {
		ev.Base = &core.BaseRun{Failed: b.Failed, Passed: names("p", b.Passed), Skipped: names("s", b.Skipped), Errored: names("e", b.Errored), RequireTest: b.RequireTest}
	}
	for i, m := range c.Mutants {
		ev.Mutants = append(ev.Mutants, core.Mutant{Path: m.Path, Level: core.Level(m.Level), Line: i, Mutator: "M", Status: m.Status})
	}
	if c.Fuzz != nil {
		ev.Fuzz = &core.FuzzRun{}
		for _, s := range c.Fuzz.Suites {
			fs := core.FuzzSuite{Sources: s.Sources}
			for i, o := range s.Cases {
				fs.Cases = append(fs.Cases, core.FuzzCase{Name: fmt.Sprintf("Fuzz%d", i), Outcome: o})
			}
			ev.Fuzz.Suites = append(ev.Fuzz.Suites, fs)
		}
	}
	if c.Coverage != nil {
		ev.Coverage = &core.Coverage{}
		for _, f := range c.Coverage.Files {
			cf := core.CoverFile{Path: f.Path, Lines: map[int]int{}}
			for _, h := range f.Hits {
				cf.Lines[h[0]] = h[1]
			}
			ev.Coverage.Files = append(ev.Coverage.Files, cf)
		}
	}
	return ev
}

func (c modelCase) coreWaivers() ([]core.Waiver, error) {
	type full struct {
		Objective string `json:"objective"`
		Scope     string `json:"scope"`
		Rationale string `json:"rationale"`
		Approver  string `json:"approver"`
		Expires   string `json:"expires"`
	}
	doc := []full{}
	for _, w := range c.Waivers {
		doc = append(doc, full{w.Objective, w.Scope, "differential test generated waiver", "drt", w.Expires})
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return core.ParseWaivers("drt-waivers.yaml", data)
}

func (c modelCase) decideGo() (core.Status, error) {
	ws, err := c.coreWaivers()
	if err != nil {
		return "", err
	}
	return core.DecideObjective(c.coreObjective(), c.coreChanged(), c.coreEvidence(), ws, c.Date).Status, nil
}
