package core

import "fmt"

type TestBudget struct {
	Files []BudgetEntry
}

type BudgetEntry struct {
	Path  string
	Level Level
	Test  bool
	Cases int
	Lines int
}

func Allowed(b Budget, lines int) int {
	return max(b.Floor, (lines+b.LinesPerCase-1)/b.LinesPerCase)
}

func (d *decision) budget(o Objective, changed []ChangedFile, tb TestBudget, active []Waiver) {
	for _, l := range levels {
		status := o.Levels[l]
		var atLevel []string
		for _, f := range changed {
			if f.Level == l && inScope(o, f) {
				atLevel = append(atLevel, f.Path)
			}
		}
		if status == "" || len(atLevel) == 0 {
			continue
		}
		var cases, lines int
		for _, e := range tb.Files {
			if e.Level != l {
				continue
			}
			if e.Test {
				cases += e.Cases
			} else {
				lines += e.Lines
			}
		}
		b, ok := o.Budget[l]
		var text string
		switch {
		case !ok:
			text = fmt.Sprintf("level %s: the catalog has no test budget for this level", l)
		case cases > Allowed(b, lines):
			text = fmt.Sprintf("level %s: %d new test cases against %d allowed for %d changed source lines", l, cases, Allowed(b, lines), lines)
		default:
			continue
		}
		cover, covered := coveringWaiver(active, atLevel)
		d.unlocated([]string{text}, status == "required", cover, covered)
	}
}
