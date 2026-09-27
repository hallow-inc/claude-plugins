package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func runGuard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agentType := fs.String("agent-type", "", "Claude Code agent_type of the editing agent; empty for the main thread")
	snapshot := fs.Bool("snapshot", false, "record protected-file hashes for --session")
	drift := fs.Bool("drift", false, "report protected files changed since the --session snapshot")
	session := fs.String("session", "", "session id for --snapshot and --drift")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *snapshot || *drift {
		return runSnapshot(*snapshot, *drift, *session, fs.NArg(), stdout, stderr)
	}
	if fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure guard [--agent-type <type>] <path>... | --snapshot|--drift --session <id>")
		return 2
	}
	g, err := app.NewGuard(nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	code := 0
	for _, p := range fs.Args() {
		ok, reason := g.Check(p, *agentType)
		if ok {
			_, _ = fmt.Fprintf(stdout, "allow\t%s\n", p)
			continue
		}
		code = 1
		_, _ = fmt.Fprintf(stdout, "deny\t%s\t%s\n", p, strings.ReplaceAll(reason, "\n", "; "))
	}
	return code
}

func runSnapshot(snapshot, drift bool, session string, nargs int, stdout, stderr io.Writer) int {
	if snapshot == drift || nargs > 0 || !core.ValidSession(session) {
		_, _ = fmt.Fprintln(stderr, "usage: assure guard --snapshot|--drift --session <id>; the id must match [A-Za-z0-9_-]{1,128}")
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 1
	}
	if snapshot {
		s, err := core.TakeSnapshot(m, session, nil)
		if err == nil {
			err = core.WriteSnapshot(m, s)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "assure: snapshot: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "snapshot\t%d protected files\n", len(s.Files))
		return 0
	}
	s, err := core.ReadSnapshot(m, session)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "drift\tsnapshot missing for session %s: %v\n", session, err)
		return 1
	}
	changes, err := core.Drift(m, s, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: drift: %v\n", err)
		return 1
	}
	for _, c := range changes {
		_, _ = fmt.Fprintf(stdout, "drift\t%s\n", c)
	}
	if len(changes) > 0 {
		return 1
	}
	return 0
}
