package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	disableInvertLogical = "mutants:\n  invert-logical:\n    enabled: false\n"
	andBase              = "package a\n\nfunc And(a, b bool) bool {\n\treturn a\n}\n"
	andTest              = "package a\n\nimport \"testing\"\n\nfunc TestAnd(t *testing.T) {\n\tif !And(true, true) || And(false, false) {\n\t\tt.Fatal(\"and\")\n\t}\n}\n"
)

func TestMutationReportsSurvivingLogicalConnectorMutant(t *testing.T) {
	needGremlins(t)
	cases := []struct {
		name  string
		files map[string]string
		setup func(t *testing.T)
	}{
		{"no outside settings", nil, func(*testing.T) {}},
		{"module .gremlins.yaml", map[string]string{".gremlins.yaml": disableInvertLogical}, func(*testing.T) {}},
		{"GREMLINS_ env var", nil, func(t *testing.T) {
			t.Setenv("GREMLINS_MUTANTS_INVERT_LOGICAL_ENABLED", "false")
		}},
		{"XDG_CONFIG_HOME config", nil, func(t *testing.T) {
			xdg := t.TempDir()
			writeFiles(t, xdg, map[string]string{filepath.Join("gremlins", "gremlins", ".gremlins.yaml"): disableInvertLogical})
			t.Setenv("XDG_CONFIG_HOME", xdg)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"a/a.go": andBase, "a/a_test.go": andTest}
			for p, b := range c.files {
				files[p] = b
			}
			dir := committedModule(t, files)
			writeFiles(t, dir, map[string]string{"a/a.go": strings.Replace(andBase, "return a\n", "return a && b\n", 1)})
			c.setup(t)
			ms := mutationReport(t, dir)
			for _, m := range ms {
				if m.Path == "a/a.go" && m.Line == 4 && m.Status == "Survived" && m.Mutator == "INVERT_LOGICAL" {
					return
				}
			}
			t.Fatalf("no Survived INVERT_LOGICAL mutant on the changed line a/a.go:4 (`return a && b`, tested only on equal inputs) with Gremlins settings from %s: %+v; the catalog requires LCR for VER-MUTATION-CHANGED, and only an && to || mutant exposes this gap", c.name, ms)
		})
	}
}

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
