package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"pgregory.net/rapid"
)

const (
	twoLangManifest = "version: 0\ncatalog: v0\nlanguages: [yy, xx]\ndefault_level: B\ncomponents: []\n"
	untrackedLint   = `{"protocol":2,"tools":[{"name":"golangci-lint","version":"2.13.2","pinned_by":"mise.local.toml","untracked":true,"install":"go install example/golangci-lint@v2.13.2"}]}`
)

func TestToolsUntrackedPinUnderLocalAuthority(t *testing.T) {
	newRepo(t, twoLangManifest)
	cannedAdapters(t, map[string]map[string]string{"yy": {"tools.json": `{"protocol":2,"tools":[]}`}, "xx": {"tools.json": untrackedLint}})
	code, stdout, stderr := assure("tools")
	f := strings.Fields(stdout)
	if code != 0 || strings.Count(stdout, "\n") != 1 || len(f) < 5 || f[0] != "xx" || f[1] != "golangci-lint" || f[2] != "2.13.2" || f[len(f)-2] != "mise.local.toml" || f[len(f)-1] != "untracked" {
		t.Fatalf("exit %d stdout %q stderr %q; a solo contributor's untracked pin is listed, marked, and accepted", code, stdout, stderr)
	}
	code, stdout, _ = assure("tools", "--install-script")
	if code != 0 || stdout != "#!/bin/sh\nset -eu\ngo install example/golangci-lint@v2.13.2\n" {
		t.Fatalf("exit %d script %q; an untracked entry's install line belongs in the script", code, stdout)
	}
}

type toolLine struct {
	lang, name, version, pinnedBy string
	untracked                     bool
}

type toolEntry struct {
	adapterproto.Tool
	Untracked bool
}

func (e toolEntry) MarshalJSON() ([]byte, error) {
	m := map[string]any{"name": e.Name, "version": e.Version, "pinned_by": e.PinnedBy, "install": e.Install}
	if e.Untracked {
		m["untracked"] = true
	}
	return json.Marshal(m)
}

var toolGen = rapid.Custom(func(rt *rapid.T) toolEntry {
	return toolEntry{adapterproto.Tool{
		Name:     rapid.StringMatching(`[a-z][a-z-]{0,12}`).Draw(rt, "name"),
		Version:  rapid.StringMatching(`[0-9]{1,2}\.[0-9]{1,2}\.[0-9]{1,2}`).Draw(rt, "version"),
		PinnedBy: rapid.SampledFrom([]string{"default", "mise.toml", "mise.local.toml", ".tool-versions", "go.mod"}).Draw(rt, "pinned_by"),
		Install:  rapid.StringMatching(`[^\n\r]{1,40}`).Draw(rt, "install"),
	}, rapid.Bool().Draw(rt, "untracked")}
})

func drawToolResponses(rt *rapid.T, bin string, langs ...string) (want []toolLine, installs string) {
	seen := map[string]bool{}
	for _, lang := range langs {
		tools := rapid.SliceOfDistinct(toolGen, func(tool toolEntry) string { return tool.Name }).Draw(rt, lang)
		data, err := json.Marshal(map[string]any{"protocol": 2, "tools": tools})
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, lang+".d", "tools.json"), data, 0o644); err != nil {
			rt.Fatal(err)
		}
		for _, tool := range tools {
			want = append(want, toolLine{lang, tool.Name, tool.Version, tool.PinnedBy, tool.Untracked})
			if !seen[tool.Install] {
				seen[tool.Install] = true
				installs += tool.Install + "\n"
			}
		}
	}
	return want, installs
}

func TestToolsPrintsAdapterResponsesInManifestOrder(t *testing.T) {
	r := newRepo(t, twoLangManifest)
	bin := cannedAdapters(t, map[string]map[string]string{"xx": {}, "yy": {}})
	rapid.Check(t, func(rt *rapid.T) {
		authority := rapid.SampledFrom([]string{"", "local", "ci"}).Draw(rt, "authority")
		manifest := twoLangManifest
		if authority != "" {
			manifest += "authority: " + authority + "\n"
		}
		if err := os.WriteFile(filepath.Join(r.root, "assurance.yaml"), []byte(manifest), 0o644); err != nil {
			rt.Fatal(err)
		}
		want, installs := drawToolResponses(rt, bin, "yy", "xx")
		if authority == "ci" && slices.ContainsFunc(want, func(w toolLine) bool { return w.untracked }) {
			checkUntrackedRefused(rt, want)
			return
		}
		script := "#!/bin/sh\nset -eu\n" + installs
		code, stdout, stderr := assure("tools", "--install-script")
		if code != 0 || stdout != script || stderr != "" {
			rt.Fatalf("exit %d stderr %q\nscript %q\nwant   %q\nCI and laptops must run exactly the adapters' distinct install lines, tracked or not, each once at its first position in manifest order, after the strict-mode header", code, stderr, stdout, script)
		}
		code, stdout, _ = assure("tools")
		got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
		if stdout == "" {
			got = nil
		}
		if code != 0 || len(got) != len(want) {
			rt.Fatalf("exit %d, %d lines for %d tools:\n%s", code, len(got), len(want), stdout)
		}
		for i, w := range want {
			if !toolLineMatches(got[i], w) {
				rt.Fatalf("line %d %q; want language, name, version, origin, then `untracked` exactly when the pin is untracked %+v", i, got[i], w)
			}
		}
	})
}

func toolLineMatches(line string, w toolLine) bool {
	f := strings.Fields(line)
	if len(f) < 4 {
		return false
	}
	origin, marked := f[len(f)-1], false
	if origin == "untracked" && len(f) > 4 {
		origin, marked = f[len(f)-2], true
	}
	return f[0] == w.lang && f[1] == w.name && f[2] == w.version && origin == w.pinnedBy && marked == w.untracked
}

func checkUntrackedRefused(rt *rapid.T, want []toolLine) {
	for _, args := range [][]string{{"tools"}, {"tools", "--install-script"}} {
		code, stdout, stderr := assure(args...)
		if code != 1 || stdout != "" {
			rt.Fatalf("%v: exit %d stdout %q; under authority: ci a pin CI cannot see must fail with nothing on stdout", args, code, stdout)
		}
		for _, w := range want {
			if w.untracked && !hasLine(stderr, w.name, w.pinnedBy) {
				rt.Fatalf("%v: no stderr line names %s and %s:\n%s", args, w.name, w.pinnedBy, stderr)
			}
		}
	}
}

func TestToolsFailuresPrintNothingOnStdout(t *testing.T) {
	cases := map[string]struct {
		manifest, tools, args string
		code                  int
		stderr                string
	}{
		"environment":                 {twoLangManifest, `{"protocol":2,"tools":[],"environment":{"message":"golangci-lint pin 2.13 is not exact"}}`, "", 1, "golangci-lint pin 2.13 is not exact"},
		"environment, install script": {twoLangManifest, `{"protocol":2,"tools":[],"environment":{"message":"golangci-lint pin 2.13 is not exact"}}`, "--install-script", 1, "golangci-lint pin 2.13 is not exact"},
		"adapter error":               {twoLangManifest, `{"protocol":1,"tools":[]}`, "--install-script", 1, "assure-adapter-xx tools: protocol mismatch"},
		"missing manifest":            {"", `{"protocol":2,"tools":[]}`, "", 2, "assurance.yaml"},
		"usage":                       {twoLangManifest, `{"protocol":2,"tools":[]}`, "--bogus", 2, "usage: assure tools"},
		"untracked pin, ci":           {twoLangManifest + "authority: ci\n", untrackedLint, "", 1, "golangci-lint"},
		"untracked pin, ci, script":   {twoLangManifest + "authority: ci\n", untrackedLint, "--install-script", 1, "mise.local.toml"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			newRepo(t, c.manifest)
			ok := `{"protocol":2,"tools":[{"name":"gremlins","version":"0.6.0","pinned_by":"default","install":"go install example/gremlins@v0.6.0"}]}`
			cannedAdapters(t, map[string]map[string]string{"yy": {"tools.json": ok}, "xx": {"tools.json": c.tools}})
			args := []string{"tools"}
			if c.args != "" {
				args = append(args, c.args)
			}
			code, stdout, stderr := assure(args...)
			if code != c.code || stdout != "" || !strings.Contains(stderr, c.stderr) {
				t.Fatalf("exit %d stdout %q stderr %q; want exit %d, empty stdout so `| sh` installs nothing, stderr naming %q", code, stdout, stderr, c.code, c.stderr)
			}
		})
	}
}
