package hallowassurance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
)

var skillUserOnly = map[string]bool{"assure-testing": false, "check": true, "bugfix": true, "inspect": true}

func skillFile(t *testing.T, p string) (map[string]any, string, bool) {
	t.Helper()
	rest, ok := strings.CutPrefix(string(read(t, p)), "---\n")
	front, body, ok2 := strings.Cut(rest, "\n---\n")
	if !ok || !ok2 {
		t.Errorf("%s: no --- frontmatter block", p)
		return nil, "", false
	}
	doc, err := decode.YAML([]byte(front))
	m, isMap := doc.Value.(map[string]any)
	if err != nil || !isMap {
		t.Errorf("%s: frontmatter does not parse as a YAML mapping (%v); Claude Code then drops every key and the skill never triggers", p, err)
		return nil, "", false
	}
	return m, body, true
}

func checkSkillFront(t *testing.T, p, dir string, m map[string]any) {
	t.Helper()
	if name, _ := m["name"].(string); name != dir {
		t.Errorf("%s: name %q, want %q", p, m["name"], dir)
	}
	if d, _ := m["description"].(string); strings.TrimSpace(d) == "" {
		t.Errorf("%s: empty description; the description is the skill's only trigger", p)
	}
	want, listed := skillUserOnly[dir]
	got, set := m["disable-model-invocation"]
	if listed && want && got != true {
		t.Errorf("%s: disable-model-invocation %v; a workflow the model starts on its own can spawn agents unasked", p, got)
	}
	if listed && !want && set {
		t.Errorf("%s sets disable-model-invocation; the testing guidance must stay model-invocable", p)
	}
}

func TestSkillFilesDeclareTheirInvocation(t *testing.T) {
	if _, err := os.Stat("plugin/commands"); err == nil {
		t.Error("plugin/commands exists; workflows ship as skills only")
	}
	paths, err := filepath.Glob("plugin/skills/*/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	for _, p := range paths {
		dir := filepath.Base(filepath.Dir(p))
		if m, body, ok := skillFile(t, p); ok {
			checkSkillFront(t, p, dir, m)
			bodies[dir] = body
		}
	}
	for dir := range skillUserOnly {
		if _, ok := bodies[dir]; !ok {
			t.Errorf("plugin/skills/%s/SKILL.md is missing or unparsable", dir)
		}
	}
	if !strings.Contains(bodies["assure-testing"], "assure reference") {
		t.Error("assure-testing does not point to `assure reference`, the only source of language-specific guidance")
	}
	for _, s := range []string{"Assure-Kind: fix", "hallow-assurance:verifier", "hallow-assurance:implementer"} {
		if !strings.Contains(bodies["bugfix"], s) {
			t.Errorf("bugfix does not contain %q", s)
		}
	}
}
