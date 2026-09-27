package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fast := fs.Bool("fast", false, "run only the objectives adapters mark fast")
	ref := fs.String("changed-from", "HEAD", "git ref whose diff to the working tree, plus untracked files, is the changed set")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure check --fast [--changed-from <ref>]")
		return 2
	}
	if !*fast {
		_, _ = fmt.Fprintln(stderr, "assure: check requires --fast; full evaluation arrives with assure evaluate in M3")
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 1
	}
	rep := app.FastCheck(m, *ref, "cli")
	if _, err := io.WriteString(stdout, rep.Render()); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	if rep.Blocking() {
		return 1
	}
	return 0
}
