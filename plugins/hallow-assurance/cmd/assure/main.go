package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `assure — deterministic verification evaluator

Usage:
  assure <command> [arguments]

Commands:
  check      Run the fast objectives on changed files
  classify   Print level, language, and role for each path
  context    Print the level map and applicable objectives
  guard      Decide whether an agent may edit each path
  hook       Handle a Claude Code hook event (JSON on stdin)
  help       Show this help
  version    Print the assure version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "help", "-h", "--help":
		_, err = fmt.Fprint(stdout, usage)
	case "version", "--version":
		_, err = fmt.Fprintln(stdout, version)
	case "guard":
		return runGuard(args[1:], stdout, stderr)
	case "context":
		return runContext(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "hook":
		return runHook(args[1:], stdout, stderr)
	case "classify":
		return runClassify(args[1:], stdout, stderr)
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
