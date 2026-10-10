package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

func runTools(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	script := fs.Bool("install-script", false, "print a POSIX shell script that installs every resolved tool")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure tools [--install-script]")
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 2
	}
	langs, problems := app.ResolveTools(m)
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintf(stderr, "assure: %s\n", p)
		}
		return 1
	}
	out := app.ToolList(langs)
	if *script {
		out = app.InstallScript(langs)
	}
	if _, err := io.WriteString(stdout, out); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	return 0
}
