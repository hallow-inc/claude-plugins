package main

import (
	"fmt"
	"io"
	"os"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/hookio"
)

var hookStdin io.Reader = os.Stdin

func runHook(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "protocol" {
		_, _ = fmt.Fprintln(stdout, hookio.Protocol)
		return 0
	}
	if len(args) != 1 || !hookio.IsEvent(args[0]) {
		_, _ = fmt.Fprintln(stderr, "usage: assure hook session-start|pre-tool-use|post-tool-use|stop|subagent-stop|protocol  (hook JSON on stdin)")
		return 2
	}
	return hookio.Run(args[0], hookStdin, stdout, stderr)
}
