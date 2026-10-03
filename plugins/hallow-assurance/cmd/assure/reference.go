package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

func runReference(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(stderr, "usage: assure reference <lang>")
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 1
	}
	refs, failures := app.References(m)
	for _, err := range failures {
		_, _ = fmt.Fprintf(stderr, "assure: warning: %v\n", err)
	}
	lang, ok := refs[args[0]]
	if !ok {
		avail := slices.Sorted(maps.Keys(refs))
		list := strings.Join(avail, ", ")
		if list == "" {
			list = "none"
		}
		_, _ = fmt.Fprintf(stderr, "assure: no installed adapter provides a %q reference; available: %s\n", args[0], list)
		return 1
	}
	out, err := adapterproto.Reference(m.Root, lang)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	return 0
}
