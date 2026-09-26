package core

import (
	"testing"

	"pgregory.net/rapid"
)

func TestRoleIsFirstMatchingListInPrecedenceOrder(t *testing.T) {
	order := []Role{Generated, FuzzCorpus, Test, Config}
	rapid.Check(t, func(t *rapid.T) {
		p := pathGen.Draw(t, "path")
		rp := RolePatterns{Patterns: map[string][]string{}}
		want := Unclassified
		for _, role := range order {
			if rapid.Bool().Draw(t, string(role)) {
				rp.Patterns[string(role)] = []string{p}
				if want == Unclassified {
					want = role
				}
			}
		}
		if rapid.Bool().Draw(t, "claimed") {
			rp.Claims = []string{p}
			if want == Unclassified {
				want = Source
			}
		}
		r, err := NewRoles(map[string]RolePatterns{"x": rp})
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.Of(p)
		if err != nil || got != want {
			t.Fatalf("patterns %v claims %v: got %s %v, want %s", rp.Patterns, rp.Claims, got, err, want)
		}
	})
}

func goRoles(t *testing.T) Roles {
	r, err := NewRoles(map[string]RolePatterns{"go": {Claims: []string{"**/*.go"}, Patterns: map[string][]string{"test": {"**/*_test.go"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRoleScenarios(t *testing.T) {
	r := goRoles(t)
	for p, want := range map[string]Role{"a/b_test.go": Test, "a/b.go": Source, "README.md": Unclassified} {
		if got, err := r.Of(p); err != nil || got != want {
			t.Errorf("%s: %s %v, want %s", p, got, err, want)
		}
	}
}

func TestConflictingAdaptersAreAmbiguous(t *testing.T) {
	r, err := NewRoles(map[string]RolePatterns{
		"a": {Claims: []string{"**/*.json"}, Patterns: map[string][]string{"config": {"**/*.json"}}},
		"b": {Claims: []string{"**/*.json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Of("x.json"); err == nil {
		t.Fatal("conflicting roles accepted")
	}
}

func TestAgreeingAdaptersAreNotAmbiguous(t *testing.T) {
	r, err := NewRoles(map[string]RolePatterns{"a": {Claims: []string{"**/*.json"}}, "b": {Claims: []string{"**/*.json"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Of("x.json"); err != nil || got != Source {
		t.Fatalf("got %s %v", got, err)
	}
}
