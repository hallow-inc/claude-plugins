package app

import (
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"pgregory.net/rapid"
)

var listedToolGen = rapid.Custom(func(rt *rapid.T) adapterproto.Tool {
	return adapterproto.Tool{
		Name:      rapid.SampledFrom([]string{"vitest", "eslint", "golangci-lint", "@vitest/coverage-v8"}).Draw(rt, "name"),
		Version:   rapid.OneOf(rapid.Just(""), rapid.StringMatching(`[0-9]{1,2}\.[0-9]{1,2}\.[0-9]{1,2}`)).Draw(rt, "version"),
		PinnedBy:  rapid.SampledFrom([]string{"package-lock.json", "mise.toml", "default"}).Draw(rt, "pinned_by"),
		Install:   rapid.SampledFrom(installPool).Draw(rt, "install"),
		Untracked: rapid.Bool().Draw(rt, "untracked"),
	}
})

var langToolsGen = rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) LangTools {
	return LangTools{
		Lang:  rapid.SampledFrom([]string{"go", "typescript"}).Draw(rt, "lang"),
		Tools: rapid.SliceOfN(listedToolGen, 0, 8).Draw(rt, "tools"),
	}
}), 0, 3)

func TestToolListPrintsNotInstalledForMissingVersion(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		langs := langToolsGen.Draw(rt, "langs")
		var want strings.Builder
		for _, l := range langs {
			for _, tool := range l.Tools {
				v := tool.Version
				if v == "" {
					v = "not installed"
				}
				want.WriteString(l.Lang + "\t" + tool.Name + "\t" + v + "\tpinned by " + tool.PinnedBy)
				if tool.Untracked {
					want.WriteString("\tuntracked")
				}
				want.WriteString("\n")
			}
		}
		if got := ToolList(langs); got != want.String() {
			rt.Fatalf("listing %q\nwant    %q\none line per tool in response order; a tool the adapter lists without a version is not installed yet, and an empty column would read as a resolved tool with a blank version; a pin from a file git does not track is marked untracked after its origin, so the developer sees which pins CI could not reproduce", got, want.String())
		}
	})
}

func TestInstallScriptIgnoresVersions(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		langs := langToolsGen.Draw(rt, "langs")
		stripped := make([]LangTools, len(langs))
		for i, l := range langs {
			tools := make([]adapterproto.Tool, len(l.Tools))
			for j, tool := range l.Tools {
				tool.Version = ""
				tools[j] = tool
			}
			stripped[i] = LangTools{Lang: l.Lang, Tools: tools}
		}
		if got, want := InstallScript(langs), InstallScript(stripped); got != want {
			rt.Fatalf("script %q with versions, %q without; a not-installed tool must contribute its install line exactly as an installed one does, or a fresh checkout cannot bootstrap", got, want)
		}
	})
}

func TestInstallScriptIncludesUntrackedEntries(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		langs := langToolsGen.Draw(rt, "langs")
		tracked := make([]LangTools, len(langs))
		for i, l := range langs {
			tools := make([]adapterproto.Tool, len(l.Tools))
			for j, tool := range l.Tools {
				tool.Untracked = false
				tools[j] = tool
			}
			tracked[i] = LangTools{Lang: l.Lang, Tools: tools}
		}
		if got, want := InstallScript(langs), InstallScript(tracked); got != want {
			rt.Fatalf("script %q with untracked entries, %q with all tracked; an untracked pin still names the version to install, and dropping its line leaves a local run without the tool", got, want)
		}
	})
}
