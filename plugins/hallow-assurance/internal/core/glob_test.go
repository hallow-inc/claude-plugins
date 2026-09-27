package core

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var (
	segGen  = rapid.StringMatching(`[a-z]{1,5}`)
	pathGen = rapid.Custom(func(t *rapid.T) string {
		return strings.Join(rapid.SliceOfN(segGen, 1, 5).Draw(t, "segs"), "/")
	})
)

func mustGlob(t interface {
	Helper()
	Fatal(...any)
}, s string) Glob {
	t.Helper()
	g, err := CompileGlob(s)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestLiteralGlobMatchesExactlyItself(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p, q := pathGen.Draw(t, "p"), pathGen.Draw(t, "q")
		g := mustGlob(t, p)
		if !g.Match(p) {
			t.Fatalf("%q does not match itself", p)
		}
		if p != q && g.Match(q) {
			t.Fatalf("literal %q matched different path %q", p, q)
		}
	})
}

func TestLeadingDoubleStarMatchesAtAnyDepth(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p, prefix := pathGen.Draw(t, "p"), pathGen.Draw(t, "prefix")
		g := mustGlob(t, "**/"+p)
		if !g.Match(p) || !g.Match(prefix+"/"+p) {
			t.Fatalf("**/%s must match %q and %q", p, p, prefix+"/"+p)
		}
	})
}

func TestStarNeverCrossesSlash(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b, c := segGen.Draw(t, "a"), segGen.Draw(t, "b"), segGen.Draw(t, "c")
		if mustGlob(t, a+"/*").Match(a + "/" + b + "/" + c) {
			t.Fatalf("%s/* matched two segments", a)
		}
	})
}

func TestGlobScenarios(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"internal/billing/**", "internal/billing/ledger/post.go", true},
		{"**/*_test.go", "main_test.go", true},
		{"internal/*.go", "internal/chat/stream.go", false},
		{"cmd/{a,b}/**", "cmd/a/main.go", false},
		{"cmd/{a,b}/**", "cmd/{a,b}/main.go", true},
		{"internal/**", "internal", true},
	}
	for _, c := range cases {
		if got := mustGlob(t, c.glob).Match(c.path); got != c.want {
			t.Errorf("%q vs %q = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
}

func TestMalformedGlobIsAnError(t *testing.T) {
	for _, s := range []string{"internal/[billing/**", "a/x\\", "[]a]"} {
		if _, err := CompileGlob(s); err == nil {
			t.Errorf("CompileGlob(%q) succeeded", s)
		}
	}
}

func TestLiteralCount(t *testing.T) {
	for glob, want := range map[string]int{"internal/billing/**": 2, "internal/**": 1, "**/billing/**": 1, "**/*_test.go": 0} {
		if got := mustGlob(t, glob).Literals(); got != want {
			t.Errorf("%q literals = %d, want %d", glob, got, want)
		}
	}
}
