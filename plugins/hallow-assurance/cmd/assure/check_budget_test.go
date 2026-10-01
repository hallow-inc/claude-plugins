package main

import (
	"strings"
	"testing"
)

func TestCheckFastRunsTestBudget(t *testing.T) {
	r := goRepo(t)
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return b + a }\n")
	code, stdout, stderr := assure("check", "--fast")
	if !strings.Contains(stdout, "\tVER-TEST-BUDGET\tgo") {
		t.Fatalf("got %d\n%s\n%s; the adapter marks VER-TEST-BUDGET fast, so the Stop check must run it", code, stdout, stderr)
	}
	if strings.Contains(stdout, "VER-ROBUST-FUZZ") || strings.Contains(stdout, "VER-COVERAGE-RESOLUTION") {
		t.Fatalf("slow objectives ran on the fast path:\n%s", stdout)
	}
}
