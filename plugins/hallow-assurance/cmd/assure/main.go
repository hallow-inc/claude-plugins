package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/hookio"
)

var buildInfo = debug.ReadBuildInfo

const usage = `assure — deterministic verification evaluator

Usage:
  assure <command> [arguments]

Commands:
  check      Run the fast objectives on changed files
  classify   Print level, language, and role for each path
  context    Print the level map and applicable objectives
  evaluate   Decide every applicable objective and write the report
  guard      Decide whether an agent may edit each path
  hook       Handle a Claude Code hook event (JSON on stdin)
  record     Append a provenance record for a pending file edit
  reference  Print an adapter's testing reference for a language
  reviews    Print a pull request's reviews file from the GitHub API (needs gh)
  tools      Print each adapter's pinned tool versions, or an install script
  help       Show this help
  version    Print the assure version and hook protocol
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

var commands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"guard":     runGuard,
	"context":   runContext,
	"check":     runCheck,
	"evaluate":  runEvaluate,
	"hook":      runHook,
	"classify":  runClassify,
	"record":    runRecord,
	"reference": runReference,
	"tools":     runTools,
	"reviews":   runReviews,
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	if cmd, ok := commands[args[0]]; ok {
		return cmd(args[1:], stdout, stderr)
	}
	var err error
	switch args[0] {
	case "help", "-h", "--help":
		_, err = fmt.Fprint(stdout, usage)
	case "version", "--version":
		_, err = fmt.Fprintf(stdout, "%s (hook protocol %d)\n", moduleVersion(), hookio.Protocol)
	default:
		_, _ = fmt.Fprintf(stderr, "assure: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	return 0
}

func moduleVersion() string {
	if info, ok := buildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
