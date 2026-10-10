package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

const checkUsage = "usage: assure check --fast [--changed-from <ref>]\n       assure check --role verifier [--changed-from <ref>]"

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fast := fs.Bool("fast", false, "run only the objectives adapters mark fast")
	role := fs.String("role", "", "run the role check for a subagent: verifier")
	ref := fs.String("changed-from", "HEAD", "git ref whose diff to the working tree, plus untracked files, is the changed set")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, checkUsage)
		return 2
	}
	if msg := checkModeError(*fast, *role); msg != "" {
		_, _ = fmt.Fprintln(stderr, msg)
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 1
	}
	rep := app.FastCheck
	if *role == "verifier" {
		rep = app.VerifierCheck
	}
	r := rep(m, *ref, "cli")
	if _, err := io.WriteString(stdout, r.Render()); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	if r.Blocking() {
		return 1
	}
	return 0
}

func checkModeError(fast bool, role string) string {
	if fast && role != "" {
		return "assure: --fast and --role are separate modes; pass one. For full evaluation run assure evaluate"
	}
	if !fast && role == "" {
		return "assure: check requires --fast or --role; for full evaluation run assure evaluate"
	}
	if role != "" && role != "verifier" {
		return checkUsage
	}
	return ""
}
