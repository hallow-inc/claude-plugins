package app

import (
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"pgregory.net/rapid"
)

var installPool = []string{"npm ci", "npm ci ", "go install example/a@v1", "go install example/b@v2", "uv sync"}

func TestInstallScriptRunsEachDistinctLineOnceInListingOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		langs := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) LangTools {
			installs := rapid.SliceOfN(rapid.SampledFrom(installPool), 0, 10).Draw(rt, "installs")
			tools := make([]adapterproto.Tool, len(installs))
			for i, in := range installs {
				tools[i] = adapterproto.Tool{Name: "t", Install: in}
			}
			return LangTools{Lang: rapid.SampledFrom([]string{"go", "typescript"}).Draw(rt, "lang"), Tools: tools}
		}), 0, 3).Draw(rt, "langs")

		want := "#!/bin/sh\nset -eu\n"
		seen := map[string]bool{}
		for _, l := range langs {
			for _, tool := range l.Tools {
				if !seen[tool.Install] {
					seen[tool.Install] = true
					want += tool.Install + "\n"
				}
			}
		}
		if got := InstallScript(langs); got != want {
			rt.Fatalf("script %q\nwant   %q\nthe script must run each distinct install line once, at its first position in the tools listing, after the strict-mode header: tools sharing `npm ci` must not reinstall per tool, and lines differing only in whitespace are different commands", got, want)
		}
	})
}
