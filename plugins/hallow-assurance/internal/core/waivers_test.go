package core

import (
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

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
