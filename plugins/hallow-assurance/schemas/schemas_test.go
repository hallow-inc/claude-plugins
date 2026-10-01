package schemas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
)

func TestUnembeddedReferencesAreRefused(t *testing.T) {
	srcs, err := embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"https://assure.invalid/schemas/v0/missing.schema.json", "https://example.com/manifest.json"} {
		withRef := map[string][]byte{}
		maps.Copy(withRef, srcs)
		withRef["manifest.schema.json"] = []byte(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id": "https://assure.invalid/schemas/v0/manifest.schema.json",
			"$ref": "` + ref + `"
		}`)
		_, err := compile(withRef)
		if err == nil || !strings.Contains(err.Error(), "not embedded") {
			t.Errorf("%s: want refusal from the embedded-only loader, got %v", ref, err)
		}
	}
}

func TestTwoViolationsAreBothReportedWithLines(t *testing.T) {
	src := "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: Z\ncomponents:\n  - level: A\n"
	vs, err := Validate(Manifest, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"/default_level": 4, "/components/0": 6}
	if len(vs) != len(want) {
		t.Fatalf("got %v", vs)
	}
	for _, v := range vs {
		if line, ok := want[v.Location]; !ok || v.Line != line {
			t.Errorf("violation %v: want line %d", v, want[v.Location])
		}
	}
}

func TestDecodeErrorsSurfaceFromValidate(t *testing.T) {
	_, err := Validate(Manifest, []byte("version: 0\nversion: 0\n"))
	var de *decode.Error
	if !errors.As(err, &de) || de.Line != 2 {
		t.Fatalf("want decode error at line 2, got %v", err)
	}
}

func TestVendoredSchemasMatchPinnedHash(t *testing.T) {
	for _, name := range []string{"sarif-schema-2.1.0.json", "mutation-testing-report-schema.json"} {
		data, err := files.ReadFile("external/" + name)
		if err != nil {
			t.Fatal(err)
		}
		pin, err := os.ReadFile("external/" + name + ".source")
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if want := "sha256: " + hex.EncodeToString(sum[:]); !strings.Contains(string(pin), want) {
			t.Errorf("vendored %s changed: %s not in pin file", name, want)
		}
	}
}

func TestUnknownKindIsAnError(t *testing.T) {
	if _, err := Validate(Kind("nope"), []byte("{}")); err == nil {
		t.Fatal("unknown kind accepted")
	}
}
