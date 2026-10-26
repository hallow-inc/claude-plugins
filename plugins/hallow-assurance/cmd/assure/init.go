package main

import (
	"fmt"
	"io"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure init > "+core.ManifestName)
		return 2
	}
	cat, err := core.LoadCatalog(core.CurrentCatalog)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	langs := adapterproto.Installed()
	if len(langs) == 0 {
		_, _ = fmt.Fprintln(stderr, "assure: warning: no assure-adapter-* on PATH; languages is empty and the manifest will not load until you fill it")
	}
	if _, err := io.WriteString(stdout, core.RenderInit(cat, langs)); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	return 0
}
