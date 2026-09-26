package core

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func provablyDisjoint(a, b Glob) bool {
	for i := 0; i < len(a.segs) && i < len(b.segs); i++ {
		x, y := a.segs[i], b.segs[i]
		if x == "**" || y == "**" {
			return false
		}
		if literal(x) && literal(y) && x != y {
			return true
		}
	}
	return false
}

func tieWarnings(comps []Component, pos map[string]decode.Pos) []schemas.Violation {
	var out []schemas.Violation
	for i := range comps {
		for j := i + 1; j < len(comps); j++ {
			a, b := comps[i].Glob, comps[j].Glob
			if a.Literals() != b.Literals() || provablyDisjoint(a, b) {
				continue
			}
			loc := fmt.Sprintf("/components/%d", j)
			out = append(out, schemas.Violation{
				Line: pos[loc].Line, Column: pos[loc].Column, Location: loc, Keyword: "tie",
				Message: fmt.Sprintf("/components/%d (%s) and /components/%d (%s) are equally specific and may overlap; overlapping paths take the stricter level", i, a, j, b),
			})
		}
	}
	return out
}

func (m *Manifest) Rel(abs string) (string, bool) {
	rel, err := filepath.Rel(m.Root, filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (m *Manifest) Resolve(rel string) (component int, level Level) {
	component, level = -1, m.DefaultLevel
	best := -1
	for i, c := range m.Components {
		if !c.Glob.Match(rel) {
			continue
		}
		n := c.Glob.Literals()
		switch {
		case n > best:
			best, component, level = n, i, c.Level
		case n == best && stricter(c.Level, level):
			component, level = i, c.Level
		}
	}
	return component, level
}
