package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func runAudit(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure audit <path | glob | file:start-end>...")
		return 2
	}
	fail := func(code int, err error) int {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return code
	}
	wd, err := os.Getwd()
	if err != nil {
		return fail(1, err)
	}
	m, err := app.ManifestFor(wd)
	if errors.Is(err, core.ErrNoManifest) {
		return fail(2, fmt.Errorf("%w; run `assure init` to adopt hallow-assurance in this repository", err))
	}
	if err != nil {
		return fail(2, err)
	}
	scope, err := app.ResolveScope(m, wd, args)
	if _, ok := errors.AsType[*app.UsageError](err); ok {
		return fail(2, err)
	}
	if err != nil {
		return fail(1, err)
	}
	outcomes, err := app.Audit(m, args, scope)
	if err != nil {
		return fail(1, err)
	}
	var b strings.Builder
	for _, o := range outcomes {
		fmt.Fprintf(&b, "%s\t%s\t%s", o.Lang, o.Objective, o.Status)
		if o.Reason != "" {
			fmt.Fprintf(&b, "\t%s", strings.Join(strings.Fields(o.Reason), " "))
		}
		b.WriteString("\n")
	}
	notAudited, err := auditSummary(&b, app.AuditFindingsPath(m.Root))
	if err != nil {
		return fail(1, err)
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(1, err)
	}
	for _, line := range notAudited {
		_, _ = fmt.Fprintf(stderr, "assure: %s\n", line)
	}
	if len(notAudited) > 0 {
		return 1
	}
	return 0
}

func auditSummary(b *strings.Builder, path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fs app.AuditFindings
	if err := json.Unmarshal(data, &fs); err != nil {
		return nil, err
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(auditCountLine(fs.Findings, path) + "\n")
	var notAudited []string
	for _, u := range slices.SortedFunc(slices.Values(fs.Unaudited), func(a, b app.AuditUnaudited) int { return strings.Compare(a.Language, b.Language) }) {
		line := "audit: " + u.Language + " not audited: " + strings.Join(strings.Fields(u.Message), " ")
		b.WriteString(line + "\n")
		notAudited = append(notAudited, line)
	}
	return notAudited, nil
}

func auditCountLine(fs []app.AuditFinding, path string) string {
	if len(fs) == 0 {
		return "audit: 0 findings in " + path
	}
	byKind := map[string]int{}
	for _, f := range fs {
		byKind[f.Kind]++
	}
	parts := make([]string, 0, len(byKind))
	for _, k := range slices.Sorted(maps.Keys(byKind)) {
		parts = append(parts, fmt.Sprintf("%d %s", byKind[k], k))
	}
	noun := "findings"
	if len(fs) == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("audit: %d %s (%s) in %s", len(fs), noun, strings.Join(parts, ", "), path)
}
