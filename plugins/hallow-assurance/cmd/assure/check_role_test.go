package main

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func drawCheckArgs(t *rapid.T) (args []string, fast bool, role string, sarif bool) {
	fast = rapid.Bool().Draw(t, "fast")
	role = rapid.OneOf(rapid.SampledFrom([]string{"-", "verifier", "inspector", "implementer", "Verifier"}), rapid.StringMatching(`[a-z]{1,8}`)).Draw(t, "role")
	sarif = rapid.Bool().Draw(t, "sarif")
	var groups [][]string
	if fast {
		groups = append(groups, []string{"--fast"})
	}
	if role != "-" {
		groups = append(groups, []string{"--role", role})
	}
	if sarif {
		groups = append(groups, []string{"--sarif", "findings.sarif"})
	}
	if rapid.Bool().Draw(t, "ref") {
		groups = append(groups, []string{"--changed-from", "HEAD"})
	}
	return slices.Concat(append([][]string{{"check"}}, rapid.Permutation(groups).Draw(t, "order")...)...), fast, role, sarif
}

func usageWanted(fast bool, role string, sarif bool) (string, bool) {
	hasRole := role != "-"
	switch {
	case sarif:
		return "usage: assure check", true
	case fast && hasRole:
		return "", true
	case !fast && !hasRole:
		return "assure evaluate", true
	case hasRole && role != "verifier":
		return "usage: assure check", true
	}
	return "", false
}

func TestCheckModeFlagsExitTwoNamingEvaluateOrUsage(t *testing.T) {
	r := newRepo(t, "")
	r.write("findings.sarif", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"hallow-assurance:inspector"}},"results":[]}]}`)
	rapid.Check(t, func(t *rapid.T) {
		args, fast, role, sarif := drawCheckArgs(t)
		code, stdout, stderr := assure(args...)
		want, usage := usageWanted(fast, role, sarif)
		if !usage {
			if code == 2 {
				t.Fatalf("%v exited 2, but it is a valid mode: %s", args, stderr)
			}
			return
		}
		if code != 2 || stdout != "" || !strings.Contains(stderr, want) {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q; want exit 2 naming %q, and no SARIF file read: the inspector role is retired", args, code, stdout, stderr, want)
		}
		if strings.Contains(want, "usage") && (!strings.Contains(stderr, "--role verifier") || strings.Contains(stderr, "inspector") || strings.Contains(stderr, "--sarif")) {
			t.Fatalf("%v: usage %q must offer --role verifier and name neither inspector nor --sarif", args, stderr)
		}
	})
}

func TestCheckRoleVerifierBlocksOnTestsNotLint(t *testing.T) {
	r := goRepo(t)
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return a + b }\n\nfunc dead() {}\n")
	if code, stdout, _ := assure("check", "--fast"); code != 1 || !strings.Contains(stdout, "fail\tCODE-ZERO-WARNINGS\tgo") {
		t.Fatalf("fixture: --fast exit %d, want a CODE-ZERO-WARNINGS failure on the unused function\n%s", code, stdout)
	}
	code, stdout, stderr := assure("check", "--role", "verifier")
	if code != 0 || strings.TrimSpace(stdout) != "pass\tVER-TESTS-PASS\tgo" {
		t.Fatalf("got %d\n%s\n%s; a source lint finding is not the verifier's to fix, and only VER-TESTS-PASS runs", code, stdout, stderr)
	}
	r.write("p/q_test.go", "package p\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"broken\") }\n")
	code, stdout, stderr = assure("check", "--role", "verifier")
	if code != 1 || !strings.Contains(stdout, "fail\tVER-TESTS-PASS\tgo") || !strings.Contains(stdout, "TestBroken") {
		t.Fatalf("got %d\n%s\n%s; a failing test must block the verifier and be named", code, stdout, stderr)
	}
}
