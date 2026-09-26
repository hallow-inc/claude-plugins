package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

const usage = `assure — deterministic verification evaluator

Usage:
  assure <command>

Commands:
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
