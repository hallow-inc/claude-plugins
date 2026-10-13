package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
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
		var wantInputs []bool
		for range rapid.IntRange(0, 4).Draw(t, "n") {
			inputs := rapid.Bool().Draw(t, "inputs")
			wantInputs = append(wantInputs, inputs)
			c := "{path: '" + globGen.Draw(t, "glob") + "', level: " + string(rapid.SampledFrom(levels).Draw(t, "lvl"))
			if inputs {
				c += ", inputs: true"
			}
			comps = append(comps, c+"}")
		}
		prov := rapid.SampledFrom([]string{"", "provenance: true\n", "provenance: false\n"}).Draw(t, "provenance")
		m, err := parseManifest("m.yaml", []byte(manifestYAML(comps...)+prov))
		if err != nil {
			t.Fatal(err)
		}
		if want := prov == "provenance: true\n"; m.Provenance != want {
			t.Fatalf("Provenance = %v for %q, want %v; an absent key must mean off", m.Provenance, prov, want)
		}
		for i, want := range wantInputs {
			if m.Components[i].Inputs != want {
				t.Fatalf("component %d Inputs = %v, want %v; fuzz scoping would select the wrong components", i, m.Components[i].Inputs, want)
			}
		}
	})
}

func TestMalformedComponentGlobIsLocated(t *testing.T) {
	_, err := parseManifest("m.yaml", []byte(manifestYAML("{path: 'internal/[billing/**', level: A}")))
	if !problemAt(err, "/components/0/path") {
		t.Fatalf("want problem at /components/0/path, got %v", err)
	}
}

func TestProtectedGlobsLoadAsWritten(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		globs := rapid.SliceOfN(globGen, 0, 4).Draw(t, "protected")
		src := manifestYAML() + "protected: ['" + strings.Join(globs, "', '") + "']\n"
		if len(globs) == 0 {
			src = manifestYAML() + "protected: []\n"
		}
		m, err := parseManifest("m.yaml", []byte(src))
		if err != nil {
			t.Fatalf("protected %q: %v; a manifest rejected over well-formed globs stops every hook decision", globs, err)
		}
		var got []string
		for _, g := range m.Protected {
			got = append(got, g.String())
		}
		if strings.Join(got, "\n") != strings.Join(globs, "\n") {
			t.Fatalf("protected %q loaded as %q; guard would protect the wrong paths", globs, got)
		}
	})
}

func TestMalformedProtectedGlobIsLocated(t *testing.T) {
	src := manifestYAML() + "protected: ['docs/**', 'internal/[billing/**']\n"
	if _, err := parseManifest("m.yaml", []byte(src)); !problemAt(err, "/protected/1") {
		t.Fatalf("want problem at /protected/1, got %v; a glob that cannot compile would protect nothing", err)
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

func addFixtureSeeds(f *testing.F, globs ...string) {
	f.Helper()
	f.Add([]byte{})
	f.Add([]byte("---\n"))
	for _, g := range globs {
		files, err := filepath.Glob(g)
		if err != nil {
			f.Fatal(err)
		}
		if len(files) == 0 {
			f.Fatalf("no seed files match %s", g)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
		}
	}
}

func TestDocumentMarkerAloneIsNotAManifest(t *testing.T) {
	if m, err := ParseManifest("m.yaml", []byte("---\n")); err == nil {
		t.Fatalf("want error for a bare document marker, got manifest %+v", m)
	}
}

func FuzzParseManifest(f *testing.F) {
	addFixtureSeeds(f,
		"../../schemas/testdata/manifest/valid/*.yaml",
		"../../schemas/testdata/manifest/invalid/*.yaml",
		"../../"+ManifestName,
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest("m.yaml", data)
		if err != nil {
			return
		}
		if m == nil {
			t.Fatal("nil manifest with nil error")
		}
		vs, verr := schemas.Validate(schemas.Manifest, data)
		if verr != nil || len(vs) > 0 {
			t.Fatalf("manifest accepted but schema rejects it: err=%v violations=%v", verr, vs)
		}
	})
}

func TestNoManifest(t *testing.T) {
	if _, err := FindManifest(t.TempDir()); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("got %v", err)
	}
}
