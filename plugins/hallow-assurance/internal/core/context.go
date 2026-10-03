package core

import (
	"fmt"
	"slices"
	"strings"
)

func (m *Manifest) ProtectedList() []string {
	out := []string{ManifestName, ".assure/**"}
	for _, c := range m.Components {
		if c.Formal {
			out = append(out, c.Challenge)
		}
	}
	for _, g := range m.Protected {
		out = append(out, g.String())
	}
	return out
}

func writeObjectives(b *strings.Builder, objs []Objective, level Level, appliesTo string) {
	for _, status := range []string{"required", "advisory"} {
		for _, o := range objs {
			if (o.AppliesTo == appliesTo || appliesTo == "" && o.AppliesTo == "fix") && o.Levels[level] == status {
				title := o.Title
				if o.AppliesTo == "fix" {
					title += " (commits with trailer Assure-Kind: fix)"
				}
				fmt.Fprintf(b, "    %-24s %-8s %s\n", o.ID, status, title)
			}
		}
	}
}

func (m *Manifest) selection(only []string) (comps map[int]bool, levelsSeen map[Level]bool, useDefault bool) {
	comps, levelsSeen, useDefault = map[int]bool{}, map[Level]bool{}, only == nil
	if only == nil {
		for i, c := range m.Components {
			comps[i] = true
			levelsSeen[c.Level] = true
		}
		levelsSeen[m.DefaultLevel] = true
	}
	for _, p := range only {
		i, l := m.Resolve(p)
		levelsSeen[l] = true
		if i < 0 {
			useDefault = true
		} else {
			comps[i] = true
		}
	}
	return comps, levelsSeen, useDefault
}

func componentLine(c Component) string {
	var tags []string
	if c.Formal {
		tags = append(tags, "formal")
	}
	if c.DST {
		tags = append(tags, "dst")
	}
	suffix := ""
	if len(tags) > 0 {
		suffix = " [" + strings.Join(tags, ", ") + "]"
	}
	return fmt.Sprintf("  %s  level %s%s\n", c.Glob, c.Level, suffix)
}

func RenderContext(m *Manifest, only []string) string {
	comps, levelsSeen, useDefault := m.selection(only)
	var b strings.Builder
	fmt.Fprintf(&b, "hallow-assurance context (catalog %s)\n\n", m.Catalog.Version)
	if useDefault {
		fmt.Fprintf(&b, "Default level: %s (paths no component matches)\n", m.DefaultLevel)
	}
	if len(comps) > 0 {
		b.WriteString("Components (most literal glob segments wins; ties take the stricter level):\n")
		for i, c := range m.Components {
			if !comps[i] {
				continue
			}
			b.WriteString(componentLine(c))
		}
	}
	fmt.Fprintf(&b, "Protected (agents may not edit): %s\n", strings.Join(m.ProtectedList(), ", "))
	for _, l := range levels {
		if !levelsSeen[l] {
			continue
		}
		fmt.Fprintf(&b, "\nObjectives at level %s:\n", l)
		writeObjectives(&b, m.Catalog.Objectives, l, "")
	}
	for i, c := range m.Components {
		if !comps[i] {
			continue
		}
		for _, block := range []struct {
			on   bool
			name string
		}{{c.Formal, "formal"}, {c.DST, "dst"}} {
			if block.on {
				fmt.Fprintf(&b, "\nObjectives for %s (%s, level %s):\n", c.Glob, block.name, c.Level)
				writeObjectives(&b, m.Catalog.Objectives, c.Level, block.name)
			}
		}
	}
	return b.String()
}

func RenderReferences(refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	refs = slices.Sorted(slices.Values(refs))
	var b strings.Builder
	b.WriteString("\nTesting references (language-specific detail for the assure-testing skill):\n")
	for _, r := range refs {
		fmt.Fprintf(&b, "  %-12s assure reference %s\n", r, r)
	}
	return b.String()
}
