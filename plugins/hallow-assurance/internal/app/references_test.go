package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func referenceAdapter(lang, behavior string) string {
	describe := func(langs, ref string) string {
		return fmt.Sprintf(`#!/bin/sh
[ "$1" = describe ] || exit 1
printf '%%s' '{"protocol":1,"languages":[%s],"claims":["**/*.%s"],"patterns":{},"objectives":{}%s}'
`, langs, lang, ref)
	}
	switch behavior {
	case "broken":
		return "#!/bin/sh\necho broken >&2\nexit 1\n"
	case "plain":
		return describe(`"`+lang+`"`, "")
	case "self":
		return describe(`"`+lang+`"`, `,"reference":"`+lang+`"`)
	default:
		return describe(`"`+lang+`","`+lang+`ref"`, `,"reference":"`+lang+`ref"`)
	}
}

func drawReferenceAdapters(t *rapid.T, bin string, pool []string) map[string]string {
	behaviors := map[string]string{}
	for _, lang := range pool {
		exe := filepath.Join(bin, "assure-adapter-"+lang)
		behaviors[lang] = rapid.SampledFrom([]string{"missing", "broken", "plain", "self", "other"}).Draw(t, "behavior "+lang)
		if err := os.RemoveAll(exe); err != nil {
			t.Fatalf("%v", err)
		}
		if behaviors[lang] != "missing" {
			if err := os.WriteFile(exe, []byte(referenceAdapter(lang, behaviors[lang])), 0o755); err != nil {
				t.Fatalf("%v", err)
			}
		}
	}
	return behaviors
}

func expectReferences(langs []string, behaviors map[string]string) (want map[string]string, failing []string, described bool) {
	want = map[string]string{}
	for _, lang := range langs {
		behavior := behaviors[lang]
		described = described || behavior != "missing"
		switch behavior {
		case "missing", "broken":
			failing = append(failing, "assure-adapter-"+lang)
		case "self":
			want[lang] = lang
		case "other":
			want[lang+"ref"] = lang
		}
	}
	return want, failing, described
}

func TestReferencesMapEachReferenceToItsLanguageAndNameEveryFailure(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := []string{"xx", "yy", "zz"}
	rapid.Check(t, func(t *rapid.T) {
		langs := rapid.SliceOfNDistinct(rapid.SampledFrom(pool), 1, len(pool), rapid.ID).Draw(t, "langs")
		if err := os.RemoveAll(filepath.Join(root, ".assure")); err != nil {
			t.Fatalf("%v", err)
		}
		cacheBroken := rapid.Bool().Draw(t, "cacheBroken")
		if cacheBroken {
			putFile(t, root, ".assure/state", "not a directory\n")
		}
		putFile(t, root, "assurance.yaml", fmt.Sprintf("version: 0\ncatalog: v0\nlanguages: [%s]\ndefault_level: B\ncomponents: []\n", strings.Join(langs, ", ")))
		want, failing, described := expectReferences(langs, drawReferenceAdapters(t, bin, pool))
		m, err := ManifestFor(root)
		if err != nil {
			t.Fatalf("%v", err)
		}
		got, failures := References(m)
		if !maps.Equal(got, want) {
			t.Fatalf("references %v, want %v: each reference names the manifest language whose adapter declared it, and an adapter without one adds nothing", got, want)
		}
		wantFailures := len(failing)
		if cacheBroken && described {
			wantFailures++
		}
		if len(failures) != wantFailures {
			t.Fatalf("failures %v, want %d naming %v (cache unwritable: %v); every describe failure must reach the caller", failures, wantFailures, failing, cacheBroken && described)
		}
		for i, name := range failing {
			if !strings.Contains(failures[i].Error(), name) {
				t.Fatalf("failure %d = %v, want it to name %s, in manifest language order", i, failures[i], name)
			}
		}
	})
}
