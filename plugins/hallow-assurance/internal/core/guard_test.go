package core

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Transcribed from the role table in specs/assure-guard, independently of guard.go's table.
var specTable = map[string]map[Role]string{
	"hallow-assurance:implementer": {"source": "allow", "config": "allow", "test": "deny", "fuzz_corpus": "deny", "generated": "C-D", "unclassified": "allow"},
	"hallow-assurance:verifier":    {"source": "deny", "config": "deny", "test": "allow", "fuzz_corpus": "allow", "generated": "deny", "unclassified": "allow"},
	"hallow-assurance:pruner":      {"source": "deny", "config": "deny", "test": "allow", "fuzz_corpus": "allow", "generated": "deny", "unclassified": "allow"},
	"hallow-assurance:inspector":   {"source": "deny", "config": "deny", "test": "deny", "fuzz_corpus": "deny", "generated": "deny", "unclassified": "deny"},
	"other":                        {"source": "allow", "config": "allow", "test": "C-D", "fuzz_corpus": "C-D", "generated": "C-D", "unclassified": "allow"},
}

var (
	agentGen = rapid.SampledFrom([]string{"", Implementer, Verifier, Pruner, Inspector, "general-purpose", "hallow-assurance:unknown", "Hallow-Assurance:verifier"})
	roleGen  = rapid.SampledFrom([]Role{Source, Config, Test, FuzzCorpus, Generated, Unclassified})
	levelG   = rapid.SampledFrom(levels)
)

func TestDecisionMatchesSpecTable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		agent, level, role, prot := agentGen.Draw(t, "agent"), levelG.Draw(t, "level"), roleGen.Draw(t, "role"), rapid.Bool().Draw(t, "protected")
		row, ok := specTable[agent]
		if !ok {
			row = specTable["other"]
		}
		want := row[role] == "allow" || (row[role] == "C-D" && (level == "C" || level == "D"))
		if prot {
			want = false
		}
		got, reason := Decide(agent, level, role, prot)
		if got != want {
			t.Fatalf("Decide(%q, %s, %s, protected=%v) = %v, spec says %v", agent, level, role, prot, got, want)
		}
		if !got && reason == "" {
			t.Fatal("deny without a reason")
		}
	})
}

func TestMainThreadTestDenialPointsAtVerifier(t *testing.T) {
	_, reason := Decide("", "B", Test, false)
	if !strings.Contains(reason, Verifier) {
		t.Fatalf("reason %q does not tell the agent to spawn the verifier", reason)
	}
}

var challengeGen = rapid.Custom(func(t *rapid.T) string { return "formal/" + pathGen.Draw(t, "c") + ".lean" })

func TestProtectedPathsAreDeniedToEveryAgent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		challenge := challengeGen.Draw(t, "challenge")
		extra := pathGen.Draw(t, "extra")
		m := &Manifest{
			Components: []Component{{Glob: mustGlob(t, "formal/**"), Level: "C", Formal: true, Challenge: "./" + challenge}},
			Protected:  []Glob{mustGlob(t, extra+"/**")},
		}
		target := rapid.SampledFrom([]string{
			ManifestName,
			".assure/" + pathGen.Draw(t, "under"),
			".assure/state/adapters.json",
			challenge,
			extra + "/" + pathGen.Draw(t, "below"),
		}).Draw(t, "target")
		if !m.IsProtected(target) {
			t.Fatalf("%q not protected", target)
		}
		agent, level, role := agentGen.Draw(t, "agent"), levelG.Draw(t, "level"), roleGen.Draw(t, "role")
		if ok, _ := Decide(agent, level, role, m.IsProtected(target)); ok {
			t.Fatalf("%s allowed to edit protected %q", agent, target)
		}
	})
}

func TestOrdinaryPathsAreNotProtected(t *testing.T) {
	m := &Manifest{Protected: []Glob{mustGlob(t, ".github/workflows/**")}}
	for _, p := range []string{"internal/a.go", "assurance.yaml.bak", ".assurex/y", "sub/assurance.yaml"} {
		if m.IsProtected(p) {
			t.Errorf("%q protected", p)
		}
	}
	if !m.IsProtected(".github/workflows/ci.yml") {
		t.Error("manifest protected glob ignored")
	}
}
