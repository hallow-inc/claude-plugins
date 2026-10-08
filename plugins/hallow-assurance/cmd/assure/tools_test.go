package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"pgregory.net/rapid"
)

const twoLangManifest = "version: 0\ncatalog: v0\nlanguages: [yy, xx]\ndefault_level: B\ncomponents: []\n"

type toolLine struct{ lang, name, version, pinnedBy string }

var toolGen = rapid.Custom(func(rt *rapid.T) adapterproto.Tool {
	return adapterproto.Tool{
		Name:     rapid.StringMatching(`[a-z][a-z-]{0,12}`).Draw(rt, "name"),
		Version:  rapid.StringMatching(`[0-9]{1,2}\.[0-9]{1,2}\.[0-9]{1,2}`).Draw(rt, "version"),
		PinnedBy: rapid.SampledFrom([]string{"default", "mise.toml", ".tool-versions", "go.mod"}).Draw(rt, "pinned_by"),
		Install:  rapid.StringMatching(`[^\n\r]{1,40}`).Draw(rt, "install"),
	}
})

func drawToolResponses(rt *rapid.T, bin string, langs ...string) (want []toolLine, installs string) {
	for _, lang := range langs {
		tools := rapid.SliceOfDistinct(toolGen, func(tool adapterproto.Tool) string { return tool.Name }).Draw(rt, lang)
		data, err := json.Marshal(map[string]any{"protocol": 1, "tools": tools})
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, lang+".d", "tools.json"), data, 0o644); err != nil {
			rt.Fatal(err)
		}
		for _, tool := range tools {
			want = append(want, toolLine{lang, tool.Name, tool.Version, tool.PinnedBy})
			installs += tool.Install + "\n"
		}
	}
	return want, installs
}

func TestToolsPrintsAdapterResponsesInManifestOrder(t *testing.T) {
	newRepo(t, twoLangManifest)
	bin := cannedAdapters(t, map[string]map[string]string{"xx": {}, "yy": {}})
	rapid.Check(t, func(rt *rapid.T) {
		want, installs := drawToolResponses(rt, bin, "yy", "xx")
		script := "#!/bin/sh\nset -eu\n" + installs
		code, stdout, stderr := assure("tools", "--install-script")
		if code != 0 || stdout != script || stderr != "" {
			rt.Fatalf("exit %d stderr %q\nscript %q\nwant   %q\nCI and laptops must run exactly the adapters' install lines, in manifest order, after the strict-mode header", code, stderr, stdout, script)
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
			f := strings.Fields(got[i])
			if len(f) < 4 || f[0] != w.lang || f[1] != w.name || f[2] != w.version || f[len(f)-1] != w.pinnedBy {
				rt.Fatalf("line %d %q; want language, name, version, then origin %+v", i, got[i], w)
			}
		}
	})
}

func TestToolsFailuresPrintNothingOnStdout(t *testing.T) {
	cases := map[string]struct {
		manifest, tools, args string
		code                  int
		stderr                string
	}{
		"environment":                 {twoLangManifest, `{"protocol":1,"tools":[],"environment":{"message":"golangci-lint pin 2.13 is not exact"}}`, "", 1, "golangci-lint pin 2.13 is not exact"},
		"environment, install script": {twoLangManifest, `{"protocol":1,"tools":[],"environment":{"message":"golangci-lint pin 2.13 is not exact"}}`, "--install-script", 1, "golangci-lint pin 2.13 is not exact"},
		"adapter error":               {twoLangManifest, `{"protocol":0,"tools":[]}`, "--install-script", 1, "assure-adapter-xx tools: protocol mismatch"},
		"missing manifest":            {"", `{"protocol":1,"tools":[]}`, "", 2, "assurance.yaml"},
		"usage":                       {twoLangManifest, `{"protocol":1,"tools":[]}`, "--bogus", 2, "usage: assure tools"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			newRepo(t, c.manifest)
			ok := `{"protocol":1,"tools":[{"name":"gremlins","version":"0.6.0","pinned_by":"default","install":"go install example/gremlins@v0.6.0"}]}`
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
