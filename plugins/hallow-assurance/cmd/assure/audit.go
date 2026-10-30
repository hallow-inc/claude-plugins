package main

import (
	"errors"
	"fmt"
	"io"
	"os"
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
	outcomes, err := app.Audit(m, scope)
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
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(1, err)
	}
	return 0
}
