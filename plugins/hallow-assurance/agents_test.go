package hallowassurance_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/hookio"
)

type agentFile struct {
	front map[string]any
	body  string
	tools []string
}

func agentFiles(t *testing.T) map[string]agentFile {
	t.Helper()
	paths, err := filepath.Glob("plugin/agents/*.md")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]agentFile{}
	for _, p := range paths {
		text := string(read(t, p))
		rest, ok := strings.CutPrefix(text, "---\n")
		front, body, ok2 := strings.Cut(rest, "\n---\n")
		if !ok || !ok2 {
			t.Fatalf("%s: no --- frontmatter block", p)
		}
		doc, err := decode.YAML([]byte(front))
		if err != nil {
			t.Fatalf("%s: frontmatter: %v", p, err)
		}
		m, ok := doc.Value.(map[string]any)
		if !ok {
			t.Fatalf("%s: frontmatter is not a mapping", p)
		}
		var tools []string
		if s, ok := m["tools"].(string); ok {
			for tool := range strings.SplitSeq(s, ",") {
				tools = append(tools, strings.TrimSpace(tool))
			}
		}
		out[p] = agentFile{m, body, tools}
	}
	return out
}

func TestAgentFilesMatchTheGuard(t *testing.T) {
	var got []string
	for p, a := range agentFiles(t) {
		name, _ := a.front["name"].(string)
		if want := strings.TrimSuffix(filepath.Base(p), ".md"); name != want {
			t.Errorf("%s: name %q, want %q", p, name, want)
		}
		got = append(got, "hallow-assurance:"+name)
		if d, _ := a.front["description"].(string); len(strings.Fields(d)) < 5 {
			t.Errorf("%s: description %q does not say when to delegate", p, d)
		}
		for _, k := range []string{"hooks", "mcpServers", "permissionMode", "isolation"} {
			if _, ok := a.front[k]; ok {
				t.Errorf("%s sets %s: Claude Code ignores the first three for plugin agents, and a worktree writes provenance outside the PR", p, k)
			}
		}
		checkRoleContract(t, p, name, a)
		for _, tool := range []string{"Bash", "Edit", "Write"} {
			if !slices.Contains(a.tools, tool) {
				t.Errorf("%s: tools list lacks %s; every role edits files and runs tests", p, tool)
			}
		}
		checkMayEdit(t, p, name, a.body)
	}
	want := []string{core.Implementer, core.Verifier, core.Pruner}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("agent types %v, want exactly the guard's %v", got, want)
	}
}

func checkRoleContract(t *testing.T, p, name string, a agentFile) {
	t.Helper()
	skills, _ := a.front["skills"].([]any)
	preloads := slices.ContainsFunc(skills, func(s any) bool { return s == "assure-testing" || s == "hallow-assurance:assure-testing" })
	if (name == "verifier" || name == "pruner") && !preloads {
		t.Errorf("%s: skills %v does not preload assure-testing, so the first test edit runs without the testing guidance", p, a.front["skills"])
	}
}

func checkMayEdit(t *testing.T, p, name, body string) {
	t.Helper()
	mayEdit, _, ok := strings.Cut(body, "You may not")
	if !ok {
		t.Errorf("%s: prompt never says which roles it may not edit", p)
		return
	}
	for _, r := range []core.Role{core.Source, core.Config, core.Test, core.FuzzCorpus, core.Generated} {
		allowed, _ := core.Decide("hallow-assurance:"+name, "D", r, false)
		if allowed != strings.Contains(mayEdit, "`"+string(r)+"`") {
			t.Errorf("%s: prompt's may-edit sentence and guard disagree on %s (guard allows at D: %v)", p, r, allowed)
		}
	}
}

var shimExpected = regexp.MustCompile(`(?m)^expected=(\d+)$`)

func TestShimSpeaksTheBinarysHookProtocol(t *testing.T) {
	m := shimExpected.FindStringSubmatch(string(read(t, "plugin/bin/assure-hook")))
	if m == nil || m[1] != strconv.Itoa(hookio.Protocol) {
		t.Fatalf("shim expects %v, assure implements hook protocol %d", m, hookio.Protocol)
	}
	path := fakeAssure(t, `if [ "$1 $2" = "hook protocol" ]; then echo 0; exit 0; fi
echo ran; exit 0`)
	code, out, errb := shimEvent(t, "post-tool-use", adoptedDir(t), path, "{}")
	if code != 2 || strings.Contains(out, "ran") || !strings.Contains(errb, "'0'") {
		t.Fatalf("an assure without post-tool-use was run: %d %q %q", code, out, errb)
	}
}
