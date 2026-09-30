package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func runBaseline(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure baseline")
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 2
	}
	b, err := app.Baseline(m)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: baseline not written:\n%v\n", err)
		return 2
	}
	path := filepath.Join(m.Root, core.BaselineFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	if err := os.WriteFile(path, b.Marshal(), 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "wrote %d entries to %s\n", len(b.Entries), core.BaselineFile)
	return 0
}
