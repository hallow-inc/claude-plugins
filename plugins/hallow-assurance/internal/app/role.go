package app

import (
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const VerifierObjective = "VER-TESTS-PASS"

func VerifierCheck(m *core.Manifest, ref, label string) Report {
	return check(m, ref, label, func(id string, _ adapterproto.Objective) bool { return id == VerifierObjective })
}
