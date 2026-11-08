package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const disableBoundaryAndNegation = "mutants:\n  conditionals-boundary:\n    enabled: false\n  conditionals-negation:\n    enabled: false\n"

func TestMutationIgnoresOutsideGremlinsSettings(t *testing.T) {
	needGremlins(t)
	cases := []struct {
		name  string
		files map[string]string
		setup func(t *testing.T)
	}{
		{"module .gremlins.yaml", map[string]string{".gremlins.yaml": disableBoundaryAndNegation}, func(*testing.T) {}},
		{"GREMLINS_ env var", nil, func(t *testing.T) {
			t.Setenv("GREMLINS_MUTANTS_CONDITIONALS_BOUNDARY_ENABLED", "false")
			t.Setenv("GREMLINS_MUTANTS_CONDITIONALS_NEGATION_ENABLED", "false")
		}},
		{"XDG_CONFIG_HOME config", nil, func(t *testing.T) {
			xdg := t.TempDir()
			writeFiles(t, xdg, map[string]string{filepath.Join("gremlins", "gremlins", ".gremlins.yaml"): disableBoundaryAndNegation})
			t.Setenv("XDG_CONFIG_HOME", xdg)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"a/a.go": boundarySrc, "a/a_test.go": boundaryTest}
			for p, b := range c.files {
				files[p] = b
			}
			dir := committedModule(t, files)
			writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(boundarySrc, "return n > 0", "return n > 0 && n < 1000", 1)})
			c.setup(t)
			ms := mutationReport(t, dir)
			for _, m := range ms {
				if m.Path == "a/a.go" && m.Line == 4 && m.Status == "Survived" {
					return
				}
			}
			t.Fatalf("no Survived mutant on the changed line a/a.go:4 with Gremlins settings from %s: %+v; the boundary of `n > 0 && n < 1000` is untested, and a .gremlins.yaml or GREMLINS_ variable outside the manifest must not switch mutators off and hide that survivor from VER-MUTATION-CHANGED", c.name, ms)
		})
	}
}
