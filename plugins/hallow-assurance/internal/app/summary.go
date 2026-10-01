package app

import (
	"fmt"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func shortSHA(s string) string { return s[:min(len(s), 12)] }

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

func (r EvalReport) Summary() string {
	var b strings.Builder
	b.WriteString("## assure evaluate\n\n")
	fmt.Fprintf(&b, "Commit `%s`, %d changed files since `%s` (`%s`), catalog %s, date %s.\n",
		shortSHA(r.Commit), r.ChangedFiles, cell(r.ChangedFrom.Ref), shortSHA(r.ChangedFrom.SHA), r.Catalog, r.Date)
	if len(r.Objectives) > 0 {
		b.WriteString("\n| Objective | Language | Status |\n|---|---|---|\n")
		for _, e := range r.Objectives {
			lang := e.Language
			if lang == "" {
				lang = "—"
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", e.Objective, lang, e.Status)
		}
	}
	if len(r.Problems) > 0 {
		b.WriteString("\n### Problems\n\n")
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "- %s\n", indent(p))
		}
	}
	for _, e := range r.Objectives {
		if e.Status == core.Pass {
			continue
		}
		title := e.Objective
		if e.Language != "" {
			title += " (" + e.Language + ")"
		}
		fmt.Fprintf(&b, "\n### %s: %s\n\n", title, e.Status)
		for i, d := range e.Details {
			if i == maxDetails {
				fmt.Fprintf(&b, "- … %d more\n", len(e.Details)-maxDetails)
				break
			}
			fmt.Fprintf(&b, "- %s\n", indent(d))
		}
	}
	if len(r.ExpiredWaivers) > 0 {
		b.WriteString("\n### Expired waivers\n\n")
		for _, w := range r.ExpiredWaivers {
			fmt.Fprintf(&b, "- %s, scope `%s`, approver %s, expired %s\n", w.Objective, w.Scope, w.Approver, w.Expires)
		}
	}
	if len(r.RemovableBaseline) > 0 {
		b.WriteString("\n### Removable baseline entries\n\n")
		for _, x := range r.RemovableBaseline {
			e := x.Entry
			fmt.Fprintf(&b, "- %s `%s` in `%s`: %s (%d of %d unused)\n", e.Objective, e.Rule, e.Path, indent(e.Message), x.Unused, e.Count)
		}
	}
	r.resolutionSummary(&b)
	return b.String()
}

func (r EvalReport) resolutionSummary(b *strings.Builder) {
	var applied []core.Resolution
	for _, e := range r.Objectives {
		applied = append(applied, e.Resolutions...)
	}
	if len(applied) > 0 {
		b.WriteString("\n### Applied coverage resolutions\n\n")
		for _, x := range applied {
			fmt.Fprintf(b, "- `%s` %s: %s, approver %s: %s\n", x.Path, x.Function, x.Resolution, x.Approver, indent(x.Rationale))
		}
	}
	if len(r.RemovableResolutions) > 0 {
		b.WriteString("\n### Removable coverage resolutions\n\n")
		for _, x := range r.RemovableResolutions {
			fmt.Fprintf(b, "- `%s` %s: %s, approver %s\n", x.Path, x.Function, x.Resolution, x.Approver)
		}
	}
}

func indent(s string) string { return strings.ReplaceAll(s, "\n", "\n  ") }
