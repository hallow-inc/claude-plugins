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
	var b strings.Builder
	for _, f := range scope {
		ranges := "-"
		if len(f.Ranges) > 0 {
			rs := make([]string, len(f.Ranges))
			for i, r := range f.Ranges {
				rs[i] = fmt.Sprintf("%d-%d", r[0], r[1])
			}
			ranges = strings.Join(rs, ",")
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", f.Path, f.Lang, f.Role, ranges)
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(1, err)
	}
	return 0
}
