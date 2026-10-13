package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func TestLoadWaiversReadsTheRepoFile(t *testing.T) {
	root := t.TempDir()
	body := waiverYAML("CODE-NO-UNSAFE", "legacy/**", "2026-12-31") + waiverYAML("CODE-NO-UNSAFE", "old/**", "2026-11-30")
	writeFile(t, root, WaiversFile, body)
	got, err := LoadWaivers(root)
	want := mustWaivers(t, body)
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %+v, %v, want %+v; waivers on disk must take effect", got, err, want)
	}
	for i := range want {
		if got[i].Objective != want[i].Objective || compareWaivers(got[i], want[i]) != 0 || got[i].Match("legacy/a.go") != want[i].Match("legacy/a.go") {
			t.Fatalf("waiver %d loaded as %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestUnreadableWaiversFileIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, WaiversFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if ws, err := LoadWaivers(root); err == nil {
		t.Fatalf("got %+v, nil; a waivers file that cannot be read must be reported, not treated as absent", ws)
	}
}

func FuzzParseWaivers(f *testing.F) {
	addFixtureSeeds(f,
		"../../schemas/testdata/waivers/valid/*.yaml",
		"../../schemas/testdata/waivers/invalid/*.yaml",
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		ws, err := ParseWaivers(WaiversFile, data)
		if err != nil {
			return
		}
		vs, verr := schemas.Validate(schemas.Waivers, data)
		if verr != nil || len(vs) > 0 {
			t.Fatalf("waivers accepted but schema rejects them: err=%v violations=%v", verr, vs)
		}
		for i, w := range ws {
			if _, err := CompileGlob(w.Scope); err != nil {
				t.Fatalf("waiver %d scope %q does not compile: %v", i, w.Scope, err)
			}
			if w.glob.String() != w.Scope {
				t.Fatalf("waiver %d matches with glob %q, not its scope %q", i, w.glob.String(), w.Scope)
			}
		}
	})
}
