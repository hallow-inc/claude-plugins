package core

import (
	"slices"
	"testing"

	"pgregory.net/rapid"
)

var operatorClasses = []string{"AOR", "ROR", "LCR", "ABS", "UOI"}

func TestStarterCatalogRequiresTheMutationOperatorFloor(t *testing.T) {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range c.Objectives {
		if o.ID == "VER-MUTATION-CHANGED" {
			found = true
			if !slices.Equal(o.RequiredOperators, []string{"AOR", "ROR", "LCR"}) {
				t.Errorf("VER-MUTATION-CHANGED requires %v; DATA-236 fixed the floor at [AOR ROR LCR], so an adapter lacking LCR would pass", o.RequiredOperators)
			}
			continue
		}
		if len(o.RequiredOperators) > 0 {
			t.Errorf("%s requires operator classes %v; only VER-MUTATION-CHANGED scores mutants", o.ID, o.RequiredOperators)
		}
	}
	if !found {
		t.Fatal("VER-MUTATION-CHANGED missing from catalog v0")
	}
}

func TestRequiredOperatorsKeepCatalogOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		objs := catalogDoc(t)
		want := make([][]string, len(objs))
		for i, o := range objs {
			if rapid.Bool().Draw(t, "hasops") {
				want[i] = rapid.SliceOfNDistinct(rapid.SampledFrom(operatorClasses), 1, len(operatorClasses), rapid.ID[string]).Draw(t, "ops")
				o["required_operators"] = want[i]
			}
		}
		c, err := parseCatalog("objectives.yaml", mustJSON(objs))
		if err != nil {
			t.Fatal(err)
		}
		for i, o := range c.Objectives {
			if !slices.Equal(o.RequiredOperators, want[i]) {
				t.Fatalf("%s: RequiredOperators %v, catalog lists %v", o.ID, o.RequiredOperators, want[i])
			}
		}
	})
}

func TestMissingOperatorsIsRequiredMinusDeclaredInRequiredOrder(t *testing.T) {
	classGen := rapid.OneOf(rapid.SampledFrom(operatorClasses), rapid.StringMatching(`[A-Z]{3}`))
	rapid.Check(t, func(t *rapid.T) {
		required := rapid.SliceOfDistinct(classGen, rapid.ID[string]).Draw(t, "required")
		declared := rapid.SliceOf(classGen).Draw(t, "declared")
		if rapid.Bool().Draw(t, "superset") {
			declared = append(declared, required...)
			declared = rapid.Permutation(declared).Draw(t, "shuffled")
		}
		reqCopy, decCopy := slices.Clone(required), slices.Clone(declared)

		got := MissingOperators(required, declared)

		if !slices.Equal(required, reqCopy) || !slices.Equal(declared, decCopy) {
			t.Fatalf("MissingOperators modified its inputs: required %v -> %v, declared %v -> %v", reqCopy, required, decCopy, declared)
		}
		declaredSet := map[string]bool{}
		for _, d := range declared {
			declaredSet[d] = true
		}
		superset := true
		for _, r := range required {
			if !declaredSet[r] {
				superset = false
			}
		}
		if superset != (len(got) == 0) {
			t.Fatalf("required %v declared %v: missing %v, but declared ⊇ required is %v", required, declared, got, superset)
		}
		pos := -1
		for _, g := range got {
			i := slices.Index(required, g)
			if i < 0 {
				t.Fatalf("missing %v names %q, which is not required (%v)", got, g, required)
			}
			if declaredSet[g] {
				t.Fatalf("missing %v names %q, which is declared (%v)", got, g, declared)
			}
			if i <= pos {
				t.Fatalf("missing %v is not in required order %v", got, required)
			}
			pos = i
		}
		for _, r := range required {
			if !declaredSet[r] && !slices.Contains(got, r) {
				t.Fatalf("required %v declared %v: %q is undeclared but missing %v omits it", required, declared, r, got)
			}
		}
	})
}
