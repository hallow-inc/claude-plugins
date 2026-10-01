package core

import (
	"cmp"
	"fmt"
	"slices"
)

type Coverage struct {
	Files       []CoverFile
	Resolutions []Resolution
}

type CoverFile struct {
	Path  string
	Funcs []CoverFunc
	Lines map[int]int
}

type CoverFunc struct {
	Name  string
	Start int
	End   int
}

func (f CoverFile) functionAt(line int) string {
	funcs := slices.Clone(f.Funcs)
	slices.SortStableFunc(funcs, func(a, b CoverFunc) int { return cmp.Compare(a.Start, b.Start) })
	name := ""
	for _, fn := range funcs {
		if fn.Start > line {
			break
		}
		name = fn.Name
		if fn.End != 0 && line > fn.End {
			name = ""
		}
	}
	return name
}

func coverageFindings(o Objective, changed []ChangedFile, c Coverage) (out []Finding, used []Resolution) {
	byPath := map[string]CoverFile{}
	for _, f := range c.Files {
		byPath[f.Path] = f
	}
	for _, f := range changed {
		if f.Role != Source || !inScope(o, f) || o.Levels[f.Level] == "" {
			continue
		}
		at := func(text string) {
			out = append(out, Finding{Path: f.Path, Level: f.Level, Located: true, Text: text})
		}
		cf, ok := byPath[f.Path]
		if !ok {
			at(fmt.Sprintf("%s: no coverage record for this file", f.Path))
			continue
		}
		for _, line := range f.Lines {
			if hits, ok := cf.Lines[line]; !ok || hits > 0 {
				continue
			}
			fn := cf.functionAt(line)
			if fn == "" {
				at(fmt.Sprintf("%s:%d: uncovered, outside any function, so no resolution can apply", f.Path, line))
				continue
			}
			i := slices.IndexFunc(c.Resolutions, func(r Resolution) bool { return r.Path == f.Path && r.Function == fn })
			if i < 0 {
				at(fmt.Sprintf("%s:%d: uncovered in %s, which has no coverage resolution", f.Path, line, fn))
				continue
			}
			if !slices.Contains(used, c.Resolutions[i]) {
				used = append(used, c.Resolutions[i])
			}
		}
	}
	slices.SortFunc(out, compareFindings)
	slices.SortFunc(used, compareResolutions)
	return out, used
}

func compareResolutions(a, b Resolution) int {
	return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Function, b.Function))
}

func RemovableResolutions(c Coverage) []Resolution {
	byPath := map[string]CoverFile{}
	for _, f := range c.Files {
		byPath[f.Path] = f
	}
	var out []Resolution
	for _, r := range c.Resolutions {
		cf, ok := byPath[r.Path]
		if ok && !slices.ContainsFunc(cf.Funcs, func(fn CoverFunc) bool { return fn.Name == r.Function }) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, compareResolutions)
	return out
}
