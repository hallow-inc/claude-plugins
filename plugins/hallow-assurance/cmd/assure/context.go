package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func relPaths(m *core.Manifest, args []string) ([]string, error) {
	var out []string
	for _, p := range args {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		rel, ok := m.Rel(abs)
		if !ok {
			return nil, fmt.Errorf("%s is outside the manifest root %s", p, m.Root)
		}
		out = append(out, rel)
	}
	return out, nil
}

func cwdManifest(stderr io.Writer) *core.Manifest {
	wd, err := os.Getwd()
	if err == nil {
		var m *core.Manifest
		if m, err = app.ManifestFor(wd); err == nil {
			return m
		}
	}
	_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
	return nil
}

func runContext(args []string, stdout, stderr io.Writer) int {
	m := cwdManifest(stderr)
	if m == nil {
		return 1
	}
	var only []string
	if len(args) > 0 {
		var err error
		if only, err = relPaths(m, args); err != nil {
			_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
			return 1
		}
	}
	for _, w := range m.Warnings {
		_, _ = fmt.Fprintf(stderr, "assure: warning: %s: %s\n", core.ManifestName, w)
	}
	_, failures, cacheErr := app.LoadRoles(m)
	for _, err := range failures {
		_, _ = fmt.Fprintf(stderr, "assure: warning: %v\n", err)
	}
	if cacheErr != nil {
		_, _ = fmt.Fprintf(stderr, "assure: warning: %v\n", cacheErr)
	}
	refs, _ := app.References(m)
	if _, err := io.WriteString(stdout, core.RenderContext(m, only)+core.RenderReferences(slices.Collect(maps.Keys(refs)))); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	return 0
}
