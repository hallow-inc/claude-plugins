package main

import (
	"fmt"
	"testing"
)

func TestDescribeDeclaresTheMutationOperatorFloor(t *testing.T) {
	v, _ := invoke(t, "describe")
	entry, _ := v["objectives"].(map[string]any)["VER-MUTATION-CHANGED"].(map[string]any)
	if got := fmt.Sprint(entry["operators"]); got != "[AOR ROR LCR]" {
		t.Fatalf("VER-MUTATION-CHANGED operators = %s; the catalog requires [AOR ROR LCR], and assure fails the objective without running it when any is undeclared", got)
	}
}
