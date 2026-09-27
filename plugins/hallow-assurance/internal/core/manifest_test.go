package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func manifestYAML(comps ...string) string {
	var b strings.Builder
	b.WriteString("version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: C\ncomponents:")
	if len(comps) == 0 {
		b.WriteString(" []")
	}
	b.WriteString("\n")
	for _, c := range comps {
		b.WriteString("  - " + c + "\n")
	}
	return b.String()
}

func TestTieWarnings(t *testing.T) {
	cases := []struct {
		a, b  string
		warns bool
	}{
		{"internal/**", "**/billing/**", true},
		{"internal/**", "cmd/**", false},
		{"internal/billing/**", "internal/**", false},
	}
	for _, c := range cases {
		m, err := parseManifest("m.yaml", []byte(manifestYAML("{path: '"+c.a+"', level: C}", "{path: '"+c.b+"', level: B}")))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(m.Warnings) > 0; got != c.warns {
			t.Errorf("%s + %s: warned=%v, want %v (%v)", c.a, c.b, got, c.warns, m.Warnings)
		}
		if c.warns && !strings.Contains(m.Warnings[0].Message, "/components/0") {
			t.Errorf("warning must name both components: %s", m.Warnings[0].Message)
		}
	}
}

var globGen = rapid.Custom(func(t *rapid.T) string {
	segs := rapid.SliceOfN(rapid.OneOf(segGen, rapid.Just("**"), rapid.Just("*"), rapid.StringMatching(`[a-z]{1,3}\*`)), 1, 4).Draw(t, "segs")
	return strings.Join(segs, "/")
})

func TestSchemaValidManifestWithWellFormedGlobsLoads(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var comps []string
		for range rapid.IntRange(0, 4).Draw(t, "n") {
			comps = append(comps, "{path: '"+globGen.Draw(t, "glob")+"', level: "+string(rapid.SampledFrom(levels).Draw(t, "lvl"))+"}")
		}
		if _, err := parseManifest("m.yaml", []byte(manifestYAML(comps...))); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMalformedComponentGlobIsLocated(t *testing.T) {
	_, err := parseManifest("m.yaml", []byte(manifestYAML("{path: 'internal/[billing/**', level: A}")))
	if !problemAt(err, "/components/0/path") {
		t.Fatalf("want problem at /components/0/path, got %v", err)
	}
}

func TestUnknownCatalogVersionIsLocated(t *testing.T) {
	src := strings.Replace(manifestYAML(), "catalog: v0", "catalog: v9", 1)
	if _, err := parseManifest("m.yaml", []byte(src)); !problemAt(err, "/catalog") {
		t.Fatalf("want problem at /catalog, got %v", err)
	}
}

func TestManifestFoundFromSubdirectoryWithoutCatalogFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestName), []byte(manifestYAML()), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "chat")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := FindManifest(sub)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(file)
	if err != nil {
		t.Fatal(err)
	}
	if m.Root != root || len(m.Catalog.Objectives) == 0 {
		t.Fatalf("root=%s objectives=%d", m.Root, len(m.Catalog.Objectives))
	}
}

func TestNoManifest(t *testing.T) {
	if _, err := FindManifest(t.TempDir()); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("got %v", err)
	}
}
