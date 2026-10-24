package hallowassurance_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

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

func TestAgentFilesAreOptionalHelpers(t *testing.T) {
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
		for _, k := range []string{"hooks", "mcpServers", "permissionMode"} {
			if _, ok := a.front[k]; ok {
				t.Errorf("%s sets %s, which Claude Code ignores for plugin agents", p, k)
			}
		}
		checkRoleContract(t, p, name, a)
		for _, tool := range []string{"Bash", "Edit", "Write"} {
			if !slices.Contains(a.tools, tool) {
				t.Errorf("%s: tools list lacks %s; every role edits files and runs tests", p, tool)
			}
		}
		checkEditGuidance(t, p, name, a.body)
	}
	want := []string{"hallow-assurance:implementer", "hallow-assurance:pruner", "hallow-assurance:verifier"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("agent types %v, want exactly %v", got, want)
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

var agentEdits = map[string][]string{
	"implementer": {"source", "config"},
	"verifier":    {"test", "fuzz_corpus"},
	"pruner":      {"test", "fuzz_corpus"},
}

func checkEditGuidance(t *testing.T, p, name, body string) {
	t.Helper()
	_, rest, ok := strings.Cut(body, "Edit files whose role is")
	sentence, _, _ := strings.Cut(rest, ".")
	if !ok {
		t.Errorf("%s: prompt never says which roles it edits", p)
		return
	}
	for _, r := range []string{"source", "config", "test", "fuzz_corpus"} {
		if want := slices.Contains(agentEdits[name], r); strings.Contains(sentence, "`"+r+"`") != want {
			t.Errorf("%s: edit guidance %q names `%s` = %v, want %v", p, sentence, r, !want, want)
		}
	}
	lower := strings.ToLower(body)
	for _, word := range []string{"guard", "provenance", "denies", "denied", "you may not"} {
		if strings.Contains(lower, word) {
			t.Errorf("%s: prompt says %q; nothing enforces the role any more, so the prompt must not claim it does", p, word)
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
echo EXECUTED; exit 0`)
	code, out, errb := shimEvent(t, "stop", adoptedDir(t), path, "{}")
	if code != 0 || strings.Contains(out, "EXECUTED") || !strings.Contains(out, "speaks 0") {
		t.Fatalf("an assure speaking another hook protocol was run, or the mismatch was not reported: %d %q %q", code, out, errb)
	}
}
