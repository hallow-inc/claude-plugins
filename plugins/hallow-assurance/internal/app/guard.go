package app

import (
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

type guardScope struct {
	m        *core.Manifest
	roles    core.Roles
	failure  string
	rolesErr []error
}

type Guard struct {
	extra  []core.Glob
	scopes map[string]*guardScope
}

func NewGuard(extraProtected []string) (*Guard, error) {
	g := &Guard{scopes: map[string]*guardScope{}}
	for _, s := range extraProtected {
		glob, err := core.CompileGlob(s)
		if err != nil {
			return nil, err
		}
		g.extra = append(g.extra, glob)
	}
	return g, nil
}

func (g *Guard) protected(m *core.Manifest, rel string) bool {
	if m.IsProtected(rel) {
		return true
	}
	for _, e := range g.extra {
		if e.Match(rel) {
			return true
		}
	}
	return false
}

func (g *Guard) Check(p, agentType string) (bool, string) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false, err.Error()
	}
	dir := filepath.Dir(abs)
	s, seen := g.scopes[dir]
	if !seen {
		s = &guardScope{}
		if s.m, err = ManifestFor(dir); err != nil {
			s.failure = err.Error()
		} else {
			s.roles, s.rolesErr, _ = LoadRoles(s.m)
		}
		g.scopes[dir] = s
	}
	if s.failure != "" {
		return false, s.failure
	}
	rel, inside := s.m.Rel(abs)
	if !inside {
		return false, "outside the manifest root " + s.m.Root
	}
	if g.protected(s.m, rel) {
		return core.Decide(agentType, "", "", true)
	}
	if len(s.rolesErr) > 0 {
		return false, "cannot determine file roles: " + JoinErrors(s.rolesErr)
	}
	role, err := s.roles.Of(rel)
	if err != nil {
		return false, err.Error()
	}
	_, level := s.m.Resolve(rel)
	return core.Decide(agentType, level, role, false)
}
