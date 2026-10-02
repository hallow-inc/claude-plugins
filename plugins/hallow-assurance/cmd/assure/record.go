package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func runRecord(args []string, _, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var in app.RecordInput
	fs.StringVar(&in.Session, "session", "", "session id (required)")
	fs.StringVar(&in.Tool, "tool", "", "Claude Code tool name (required)")
	fs.StringVar(&in.ToolUseID, "tool-use-id", "", "tool use id whose pending entry to record (required)")
	fs.StringVar(&in.AgentID, "agent-id", "", "agent_id of the editing subagent; empty for the main thread")
	fs.StringVar(&in.AgentType, "agent-type", "", "agent_type of the editing subagent; empty for the main thread")
	const use = "usage: assure record --session <id> --tool <name> --tool-use-id <id> [--agent-id <id>] [--agent-type <type>]"
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || in.Tool == "" ||
		!core.ValidSession(in.Session) || !core.ValidSession(in.ToolUseID) {
		_, _ = fmt.Fprintln(stderr, use)
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 2
	}
	rel, _, err := app.Record(m, in)
	switch {
	case errors.Is(err, app.ErrNoPending):
		_, _ = fmt.Fprintf(stderr, "assure: no pending entry for tool use %s; this edit is a provenance gap\n", in.ToolUseID)
		return 1
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "assure: %s: %v\n", rel, err)
		return 1
	}
	return 0
}
