package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

type guardScope struct {
	m        *core.Manifest
	roles    core.Roles
	failure  string
	rolesErr []error
}

func runGuard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agentType := fs.String("agent-type", "", "Claude Code agent_type of the editing agent; empty for the main thread")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure guard [--agent-type <type>] <path>...")
		return 2
	}
	scopes := map[string]*guardScope{}
	code := 0
	for _, p := range fs.Args() {
		ok, reason := guardOne(p, *agentType, scopes)
		if ok {
			_, _ = fmt.Fprintf(stdout, "allow\t%s\n", p)
			continue
		}
		code = 1
		_, _ = fmt.Fprintf(stdout, "deny\t%s\t%s\n", p, strings.ReplaceAll(reason, "\n", "; "))
	}
	return code
}

func guardOne(p, agentType string, scopes map[string]*guardScope) (bool, string) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false, err.Error()
	}
	dir := filepath.Dir(abs)
	s, seen := scopes[dir]
	if !seen {
		s = &guardScope{}
		if s.m, err = manifestFor(dir); err != nil {
			s.failure = err.Error()
		} else {
			s.roles, s.rolesErr, _ = loadRoles(s.m)
		}
		scopes[dir] = s
	}
	if s.failure != "" {
		return false, s.failure
	}
	rel, inside := s.m.Rel(abs)
	if !inside {
		return false, "outside the manifest root " + s.m.Root
	}
	if s.m.IsProtected(rel) {
		return core.Decide(agentType, "", "", true)
	}
	if len(s.rolesErr) > 0 {
		return false, "cannot determine file roles: " + joinErrors(s.rolesErr)
	}
	role, err := s.roles.Of(rel)
	if err != nil {
		return false, err.Error()
	}
	_, level := s.m.Resolve(rel)
	return core.Decide(agentType, level, role, false)
}
