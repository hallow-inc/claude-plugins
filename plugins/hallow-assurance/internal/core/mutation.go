package core

import (
	"cmp"
	"fmt"
	"slices"
)

type Mutant struct {
	Path    string
	Level   Level
	Line    int
	Mutator string
	Status  string
}

type BaseRun struct {
	Failed  int
	Passed  []string
	Skipped []string
	Errored []string
}

func (m Mutant) text() string {
	return fmt.Sprintf("%s:%d %s %s", m.Path, m.Line, m.Mutator, m.Status)
}

func compareMutants(a, b Mutant) int {
	return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Mutator, b.Mutator), cmp.Compare(a.Status, b.Status))
}

type mutantClass int

const (
	excluded mutantClass = iota
	detected
	undetected
	pending
)

func classify(status string) mutantClass {
	switch status {
	case "Killed", "Timeout":
		return detected
	case "Survived", "NoCoverage":
		return undetected
	case "Pending":
		return pending
	}
	return excluded
}

type levelScore struct {
	detected   int
	undetected []Mutant
	waivers    []Waiver
}

func (s *levelScore) add(c mutantClass, m Mutant) {
	if c == detected {
		s.detected++
		return
	}
	s.undetected = append(s.undetected, m)
}

func (s levelScore) fails(threshold float64, hasThreshold bool) bool {
	valid := s.detected + len(s.undetected)
	if valid == 0 {
		return false
	}
	if !hasThreshold {
		return len(s.undetected) > 0
	}
	return 100*float64(s.detected) < threshold*float64(valid)
}

func pendingMutants(ms []Mutant) []string {
	var out []string
	for _, m := range ms {
		if classify(m.Status) == pending {
			out = append(out, "mutant never ran: "+m.text())
		}
	}
	return out
}

func (d *decision) mutation(o Objective, ms []Mutant, active []Waiver) {
	ms = slices.Clone(ms)
	slices.SortFunc(ms, compareMutants)
	all, kept := map[Level]*levelScore{}, map[Level]*levelScore{}
	for _, m := range ms {
		c := classify(m.Status)
		if o.Levels[m.Level] == "" || (c != detected && c != undetected) {
			continue
		}
		if all[m.Level] == nil {
			all[m.Level], kept[m.Level] = &levelScore{}, &levelScore{}
		}
		all[m.Level].add(c, m)
		if i := slices.IndexFunc(active, func(w Waiver) bool { return w.Match(m.Path) }); i >= 0 {
			k := kept[m.Level]
			if !slices.ContainsFunc(k.waivers, func(x Waiver) bool { return compareWaivers(x, active[i]) == 0 }) {
				k.waivers = append(k.waivers, active[i])
			}
			continue
		}
		kept[m.Level].add(c, m)
	}
	for _, l := range levels {
		s := kept[l]
		if s == nil {
			continue
		}
		th, hasTh := o.Threshold[l]
		if s.fails(th, hasTh) {
			d.details = append(d.details, scoreLine(l, *s, th, hasTh))
			for _, m := range s.undetected {
				d.details = append(d.details, m.text())
			}
			d.advisory = true
			d.blocking = d.blocking || o.Levels[l] == "required"
			continue
		}
		if all[l].fails(th, hasTh) {
			for _, w := range s.waivers {
				d.waive(w, scoreLine(l, *all[l], th, hasTh))
			}
		}
	}
}

func scoreLine(l Level, s levelScore, th float64, hasTh bool) string {
	valid := s.detected + len(s.undetected)
	line := fmt.Sprintf("level %s: %d of %d mutants detected (%.1f%%)", l, s.detected, valid, 100*float64(s.detected)/float64(valid))
	if hasTh {
		return line + fmt.Sprintf(", threshold %g", th)
	}
	return line + ", no threshold: every mutant must be detected"
}

func baseRunFailures(b BaseRun) []string {
	var out []string
	if b.Failed+len(b.Passed)+len(b.Skipped)+len(b.Errored) == 0 {
		return []string{"the fix changes no test, so nothing shows the bug on the base commit"}
	}
	for _, n := range b.Passed {
		out = append(out, "passes on base: "+n)
	}
	for _, n := range b.Skipped {
		out = append(out, "skipped on base: "+n)
	}
	for _, e := range b.Errored {
		out = append(out, "error on base: "+e)
	}
	return out
}
