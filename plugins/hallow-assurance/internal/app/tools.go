package app

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

type LangTools struct {
	Lang  string
	Tools []adapterproto.Tool
}

func ResolveTools(m *core.Manifest) ([]LangTools, []string) {
	var out []LangTools
	var problems []string
	for _, lang := range m.Languages {
		t, err := adapterproto.RunTools(m.Root, lang)
		switch {
		case err != nil:
			problems = append(problems, err.Error())
		case t.Environment != nil:
			problems = append(problems, fmt.Sprintf("%s tools: %s", adapterproto.Executable(lang), t.Environment.Message))
		default:
			out = append(out, LangTools{Lang: lang, Tools: t.Tools})
		}
	}
	return out, problems
}

func ToolList(langs []LangTools) string {
	var b strings.Builder
	for _, l := range langs {
		for _, t := range l.Tools {
			fmt.Fprintf(&b, "%s\t%s\t%s\tpinned by %s", l.Lang, t.Name, cmp.Or(t.Version, "not installed"), t.PinnedBy)
			if t.Untracked {
				b.WriteString("\tuntracked")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func InstallScript(langs []LangTools) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	seen := map[string]bool{}
	for _, l := range langs {
		for _, t := range l.Tools {
			if seen[t.Install] {
				continue
			}
			seen[t.Install] = true
			b.WriteString(t.Install + "\n")
		}
	}
	return b.String()
}
