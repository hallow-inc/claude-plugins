package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"pgregory.net/rapid"
)

func TestInitRejectsAnyArgument(t *testing.T) {
	newRepo(t, "")
	rapid.Check(t, func(rt *rapid.T) {
		args := rapid.SliceOfN(rapid.StringMatching(`-{0,2}[a-z]{0,6}`), 1, 3).Draw(rt, "args")
		code, stdout, stderr := assure(append([]string{"init"}, args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: assure init") {
			rt.Fatalf("init %q: exit %d stdout %q stderr %q; init takes no arguments and must not print a template", args, code, stdout, stderr)
		}
	})
}

func TestInitWithNoAdaptersWarnsAndListsNoLanguages(t *testing.T) {
	newRepo(t, "")
	for _, path := range []string{"", t.TempDir()} {
		t.Setenv("PATH", path)
		code, stdout, stderr := assure("init")
		if code != 0 || !strings.Contains(stderr, "warning: no assure-adapter-*") || !slices.Contains(strings.Split(stdout, "\n"), "languages: []") {
			t.Fatalf("PATH %q: exit %d stderr %q; with no adapters init still prints the template, with languages: [] and a warning\n%s", path, code, stderr, stdout)
		}
	}
}

func TestInitListsTheAdaptersOnPathAndParses(t *testing.T) {
	newRepo(t, "")
	code, stdout, stderr := assure("init")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q; an adapter on PATH means no warning", code, stderr)
	}
	m, err := core.ParseManifest(core.ManifestName, []byte(stdout))
	if err != nil || !slices.Equal(m.Languages, []string{"go"}) {
		t.Fatalf("init output with assure-adapter-go on PATH must load with languages [go]: %v\n%s", err, stdout)
	}
}
