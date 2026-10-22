package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var untrackedPinOrigins = []string{"mise.local.toml", "package-lock.json", "default"}

func TestResolveToolsRefusesUntrackedPinsOnlyUnderCI(t *testing.T) {
	bin := t.TempDir()
	resp := filepath.Join(bin, "tools.json")
	script := "#!/bin/sh\n[ \"$1\" = tools ] || exit 1\ncat '" + resp + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	root := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		authority := rapid.SampledFrom([]core.Authority{core.AuthorityLocal, core.AuthorityCI}).Draw(rt, "authority")
		tools := writeToolsResponse(rt, resp)
		m := &core.Manifest{Root: root, Languages: []string{"xx"}, Authority: authority}
		langs, problems := ResolveTools(m)
		anyUntracked := slices.ContainsFunc(tools, func(x adapterproto.Tool) bool { return x.Untracked })
		if authority == core.AuthorityLocal || !anyUntracked {
			if len(problems) > 0 {
				rt.Fatalf("authority %s, tools %+v: problems %q; under local an untracked pin is listed, not refused", authority, tools, problems)
			}
			if len(langs) != 1 || langs[0].Lang != "xx" || !slices.Equal(langs[0].Tools, tools) {
				rt.Fatalf("listed %+v, want xx with %+v", langs, tools)
			}
			return
		}
		requireUntrackedProblems(rt, tools, problems)
	})
}

func writeToolsResponse(rt *rapid.T, resp string) []adapterproto.Tool {
	names := rapid.SliceOfDistinct(rapid.SampledFrom(untrackedToolNames), rapid.ID[string]).Draw(rt, "names")
	tools := []adapterproto.Tool{}
	entries := []map[string]any{}
	for _, name := range names {
		tool := adapterproto.Tool{Name: name, Version: "1.2.3", PinnedBy: rapid.SampledFrom(untrackedPinOrigins).Draw(rt, "pin "+name),
			Install: "npm ci", Untracked: rapid.Bool().Draw(rt, "untracked "+name)}
		e := map[string]any{"name": tool.Name, "version": tool.Version, "pinned_by": tool.PinnedBy, "install": tool.Install}
		if tool.Untracked {
			e["untracked"] = true
		}
		tools = append(tools, tool)
		entries = append(entries, e)
	}
	data, err := json.Marshal(map[string]any{"protocol": 2, "tools": entries})
	if err != nil {
		rt.Fatal(err)
	}
	if err := os.WriteFile(resp, data, 0o644); err != nil {
		rt.Fatal(err)
	}
	return tools
}

func requireUntrackedProblems(rt *rapid.T, tools []adapterproto.Tool, problems []string) {
	for _, tool := range tools {
		if tool.Untracked && !slices.ContainsFunc(problems, func(p string) bool {
			return strings.Contains(p, tool.Name) && strings.Contains(p, tool.PinnedBy) && strings.Contains(p, "CI cannot see")
		}) {
			rt.Fatalf("authority ci: no problem names %s with its pin file %s and says CI cannot see it:\n%s", tool.Name, tool.PinnedBy, strings.Join(problems, "\n"))
		}
	}
}

const untrackedPinAdapter = `#!/bin/sh
case "$1" in
describe) printf '%%s' '{"protocol":2,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"vitest","fast":true}}}' ;;
run)
  out=""
  while [ $# -gt 0 ]; do [ "$1" = "--out" ] && out="$2"; shift; done
  mkdir -p "$out"
  printf '%%s' '<testsuite name="p" tests="1"><testcase classname="p" name="TestFine"/></testsuite>' > "$out/junit.xml"
  printf '%%s' '{"protocol":2,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"vitest":"5.0.4"},"pinned_by":{"vitest":"package-lock.json"}%s}' ;;
*) exit 1 ;;
esac
`

func TestEvaluateFailsUntrackedPinsOnlyUnderCI(t *testing.T) {
	bin, root := verifierRepo(t)
	for _, key := range []string{"", "local", "ci"} {
		for _, untracked := range []bool{false, true} {
			name := fmt.Sprintf("authority %q untracked %v", key, untracked)
			extra := ""
			if untracked {
				extra = `,"untracked":["vitest"]`
			}
			if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), fmt.Appendf(nil, untrackedPinAdapter, extra), 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n"
			if key != "" {
				manifest += "authority: " + key + "\n"
			}
			putFile(t, root, "assurance.yaml", manifest)
			gitIn(t, root, "add", "assurance.yaml")
			gitIn(t, root, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", name)
			putFile(t, root, "a.xx", name+"\n")
			m, err := ManifestFor(root)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			rep, err := Evaluate(m, "HEAD", "2026-10-17")
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			checkUntrackedPinReport(t, name, rep, key == "ci", untracked)
		}
	}
}

func checkUntrackedPinReport(t *testing.T, name string, rep EvalReport, ci, untracked bool) {
	t.Helper()
	want := core.AuthorityLocal
	if ci {
		want = core.AuthorityCI
	}
	if rep.Authority != want {
		t.Fatalf("%s: report authority %q, want %q; an absent key means local", name, rep.Authority, want)
	}
	if rep.PinnedBy["xx"]["vitest"] != "package-lock.json" {
		t.Fatalf("%s: pinned_by %v", name, rep.PinnedBy)
	}
	if got := rep.UntrackedPins["xx"]; untracked != slices.Equal(got, []string{"vitest"}) || (!untracked && len(rep.UntrackedPins) != 0) {
		t.Fatalf("%s: untracked_pins %v; the report is the only record of which pins CI could not reproduce", name, rep.UntrackedPins)
	}
	checkUntrackedPinObjective(t, name, rep, ci, untracked)
	checkUntrackedPinSummary(t, name, rep, ci, untracked)
}

func checkUntrackedPinObjective(t *testing.T, name string, rep EvalReport, ci, untracked bool) {
	t.Helper()
	i := slices.IndexFunc(rep.Objectives, func(e Entry) bool { return e.Objective == "VER-TESTS-PASS" && e.Language == "xx" })
	if i < 0 {
		t.Fatalf("%s: no VER-TESTS-PASS entry in %+v", name, rep.Objectives)
	}
	e := rep.Objectives[i]
	if fail := ci && untracked; (e.Status == core.Fail) != fail || (!fail && e.Status != core.Pass) {
		t.Fatalf("%s: VER-TESTS-PASS %s %q; a pin CI cannot see fails its objective under authority: ci and changes nothing under local", name, e.Status, e.Details)
	}
	if ci && untracked && !slices.ContainsFunc(e.Details, func(d string) bool {
		return strings.Contains(d, "vitest") && strings.Contains(d, "package-lock.json")
	}) {
		t.Fatalf("%s: details %q do not name vitest and package-lock.json", name, e.Details)
	}
}

func checkUntrackedPinSummary(t *testing.T, name string, rep EvalReport, ci, untracked bool) {
	t.Helper()
	summary := rep.Summary()
	first, _, _ := strings.Cut(summary, "\n")
	if strings.Contains(first, "self-attested") == ci {
		t.Fatalf("%s: summary first line %q", name, first)
	}
	if at := strings.Index(strings.ToLower(summary), "untracked"); untracked && (at < 0 || !strings.Contains(summary[at:], "vitest")) {
		t.Fatalf("%s: summary does not list vitest as untracked:\n%s", name, summary)
	}
}
