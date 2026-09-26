package core

import (
	"fmt"
	"path"
	"strings"
)

const (
	Implementer = "hallow-assurance:implementer"
	Verifier    = "hallow-assurance:verifier"
	Pruner      = "hallow-assurance:pruner"
	Inspector   = "hallow-assurance:inspector"
)

type rule int

const (
	deny rule = iota
	allow
	lowOnly
)

var roleRules = map[string]map[Role]rule{
	Implementer: {Source: allow, Config: allow, Test: deny, FuzzCorpus: deny, Generated: lowOnly, Unclassified: allow},
	Verifier:    {Source: deny, Config: deny, Test: allow, FuzzCorpus: allow, Generated: deny, Unclassified: allow},
	Pruner:      {Source: deny, Config: deny, Test: allow, FuzzCorpus: allow, Generated: deny, Unclassified: allow},
	Inspector:   {},
	"":          {Source: allow, Config: allow, Test: lowOnly, FuzzCorpus: lowOnly, Generated: lowOnly, Unclassified: allow},
}

func actor(agentType string) string {
	if agentType == "" {
		return "the main thread"
	}
	return agentType
}

func Decide(agentType string, level Level, role Role, protected bool) (bool, string) {
	if protected {
		return false, "protected file: no agent may edit it (invariant 6)"
	}
	rules, known := roleRules[agentType]
	if !known {
		rules = roleRules[""]
	}
	switch rules[role] {
	case allow:
		return true, ""
	case lowOnly:
		if level == "C" || level == "D" {
			return true, ""
		}
		if agentType != Implementer && (role == Test || role == FuzzCorpus) {
			return false, fmt.Sprintf("%s files at level %s must be written by %s; spawn it for this edit", role, level, Verifier)
		}
		return false, fmt.Sprintf("%s may not edit %s files at level %s", actor(agentType), role, level)
	default:
		return false, fmt.Sprintf("%s may not edit %s files", actor(agentType), role)
	}
}

func (m *Manifest) IsProtected(rel string) bool {
	if rel == ManifestName || rel == ".assure" || strings.HasPrefix(rel, ".assure/") {
		return true
	}
	for _, c := range m.Components {
		if c.Formal && path.Clean(c.Challenge) == rel {
			return true
		}
	}
	return anyMatch(m.Protected, rel)
}
