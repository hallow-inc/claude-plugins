package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

var specRanges = map[string][2]string{
	"pytest":          {"9.0.0", "10.0.0"},
	"pytest-randomly": {"5.0.0", "6.0.0"},
	"pytest-cov":      {"7.0.0", "8.0.0"},
	"coverage":        {"7.13.0", "8.0.0"},
	"hypothesis":      {"6.130.0", "7.0.0"},
	"cosmic-ray":      {"8.7.0", "9.0.0"},
	"parso":           {"0.8.7", "0.9.0"},
}

var specToolNames = slices.Sorted(maps.Keys(specRanges))

var specObjectiveTools = map[string][]string{
	"VER-TESTS-PASS":          {"pytest", "pytest-randomly", "hypothesis"},
	"VER-MUTATION-CHANGED":    {"pytest", "pytest-randomly", "pytest-cov", "coverage", "hypothesis", "cosmic-ray", "parso"},
	"VER-FAIL-ON-BASE":        {"pytest", "pytest-randomly", "hypothesis"},
	"VER-ROBUST-FUZZ":         {"pytest", "pytest-randomly", "hypothesis"},
	"VER-COVERAGE-RESOLUTION": {"pytest", "pytest-randomly", "coverage", "hypothesis"},
}

var cosmicRayBins = []string{"cosmic-ray", "cr-filter-operators", "cr-filter-lines"}

const specInstall = "uv sync --locked"

type pep440 struct {
	release []*big.Int
	post    *big.Int
}

func digits(s string) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return nil, false
		}
	}
	n, _ := new(big.Int).SetString(s, 10)
	return n, true
}

// parseExact is the spec's exact-version shape written without a regexp:
// dot-separated ASCII decimal release segments, optionally ending in .postN.
func parseExact(v string) (pep440, bool) {
	parts := strings.Split(v, ".")
	var p pep440
	if last := parts[len(parts)-1]; strings.HasPrefix(last, "post") {
		n, ok := digits(strings.TrimPrefix(last, "post"))
		if !ok {
			return pep440{}, false
		}
		p.post = n
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return pep440{}, false
	}
	for _, s := range parts {
		n, ok := digits(s)
		if !ok {
			return pep440{}, false
		}
		p.release = append(p.release, n)
	}
	return p, true
}

func specExact(v string) bool {
	_, ok := parseExact(v)
	return ok
}

func modelCompare(a, b pep440) int {
	zero := big.NewInt(0)
	for i := range max(len(a.release), len(b.release)) {
		x, y := zero, zero
		if i < len(a.release) {
			x = a.release[i]
		}
		if i < len(b.release) {
			y = b.release[i]
		}
		if c := x.Cmp(y); c != 0 {
			return c
		}
	}
	switch {
	case a.post == nil && b.post == nil:
		return 0
	case a.post == nil:
		return -1
	case b.post == nil:
		return 1
	}
	return a.post.Cmp(b.post)
}

func mustParse(v string) pep440 {
	p, ok := parseExact(v)
	if !ok {
		panic("not exact: " + v)
	}
	return p
}

func specInRange(name, v string) bool {
	p, ok := parseExact(v)
	if !ok {
		return false
	}
	r := specRanges[name]
	return modelCompare(p, mustParse(r[0])) >= 0 && modelCompare(p, mustParse(r[1])) < 0
}

func toolNamed(t fataler, name string) pyTool {
	for _, tool := range pyTools {
		if tool.name == name {
			return tool
		}
	}
	t.Fatalf("no resolved tool %q; the spec lists it among the seven", name)
	return pyTool{}
}

var numGen = rapid.OneOf(
	rapid.Custom(func(t *rapid.T) string { return strconv.Itoa(rapid.IntRange(0, 12).Draw(t, "n")) }),
	rapid.StringMatching(`0{1,3}[0-9]{0,2}`),
	rapid.StringMatching(`[1-9][0-9]{15,30}`),
)

var exactGen = rapid.Custom(func(t *rapid.T) string {
	segs := rapid.SliceOfN(numGen, 1, 5).Draw(t, "release")
	v := strings.Join(segs, ".")
	if rapid.Bool().Draw(t, "post") {
		v += ".post" + numGen.Draw(t, "postN")
	}
	return v
})

var versionLikeGen = rapid.OneOf(
	exactGen,
	rapid.Custom(func(t *rapid.T) string {
		return exactGen.Draw(t, "v") + rapid.SampledFrom([]string{
			"a1", "b2", "rc1", ".rc1", ".dev0", "dev0", ".post", "post1", "-post1", "_post1", ".POST1", ".post1.post2",
			".post1.dev2", "+local", "+abc.1", "-1", "\n", " ", ".", ".x",
		}).Draw(t, "suffix")
	}),
	rapid.Custom(func(t *rapid.T) string {
		return rapid.SampledFrom([]string{"1!", "0!", "v", "V", " ", "=", "\n", ".", "post1."}).Draw(t, "prefix") + exactGen.Draw(t, "v")
	}),
	rapid.StringMatching(`[0-9.a-z+!\-]{0,12}`),
	rapid.String(),
	rapid.SampledFrom([]string{
		"", "6.0", "6", "1..2", ".1", "1.", "post1", ".post1", "1.post1", "1.0.post", "１.2", "1.٢", "1.0.post١",
		"1.0post1", "1.0-1", "1.0.0a1", "1.0.0.dev1", "1.0+ubuntu", "1!1.0", "8.7.0\n", " 8.7.0",
	}),
)

func TestExactVersionIsTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := versionLikeGen.Draw(t, "version")
		if got, want := isExact(v), specExact(v); got != want {
			t.Fatalf("isExact(%q) = %v, want %v; only dot-separated ASCII release numbers with an optional .postN are exact, because a pre-release, dev, local, or epoch version names no release the supported ranges were built against", v, got, want)
		}
	})
	if !isExact("6.0") {
		t.Fatal(`isExact("6.0") = false; two-part releases such as hypothesis-style "6.0" are exact per the spec`)
	}
}

func TestVersionOrderMatchesModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := exactGen.Draw(t, "a")
		b := rapid.OneOf(exactGen, rapid.Just(a), rapid.Just(a+".0"), rapid.Just(a+".post0")).Draw(t, "b")
		if !specExact(b) {
			b = a
		}
		got, want := compareVersions(a, b), modelCompare(mustParse(a), mustParse(b))
		if sign(got) != want {
			t.Fatalf("compareVersions(%q, %q) = %d, want sign %d; release segments compare numerically with missing ones as zero, and a post-release sorts after its release and before the next", a, b, got, want)
		}
	})
}

func sign(n int) int {
	return min(max(n, -1), 1)
}

// belowGen draws exact versions strictly below bound and at least every
// version below it that differs only past its last non-zero segment: the
// last non-zero segment minus one, then any huge tail and post-release.
func belowGen(bound string) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		parts := strings.Split(bound, ".")
		i := len(parts) - 1
		for parts[i] == "0" {
			i--
		}
		n, _ := strconv.Atoi(parts[i])
		parts[i] = strconv.Itoa(n - 1)
		for j := i + 1; j < len(parts); j++ {
			parts[j] = "9999999999999999999999"
		}
		tail := rapid.SliceOfN(rapid.StringMatching(`[0-9]{1,22}`), 0, 2).Draw(t, "tail")
		v := strings.Join(append(parts, tail...), ".")
		if rapid.Bool().Draw(t, "post") {
			v += ".post" + rapid.StringMatching(`[0-9]{1,22}`).Draw(t, "postN")
		}
		return v
	})
}

// equalGen draws spellings of bound itself: leading zeros and trailing zero
// segments do not change a release.
func equalGen(bound string) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		parts := strings.Split(bound, ".")
		for i := range parts {
			parts[i] = strings.Repeat("0", rapid.IntRange(0, 2).Draw(t, "zeros")) + parts[i]
		}
		for range rapid.IntRange(0, 2).Draw(t, "pad") {
			parts = append(parts, "0")
		}
		return strings.Join(parts, ".")
	})
}

func TestRangeBoundsPerTool(t *testing.T) {
	var names []string
	for _, tool := range pyTools {
		names = append(names, tool.name)
	}
	if slices.Sort(names); !slices.Equal(names, specToolNames) {
		t.Fatalf("resolved tools %q, want %q; each objective's tool set and the tools op draw from this list", names, specToolNames)
	}
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom(specToolNames).Draw(t, "tool")
		tool, r := toolNamed(t, name), specRanges[name]
		for v, want := range map[string]bool{
			equalGen(r[0]).Draw(t, "lo"):                true,
			belowGen(r[0]).Draw(t, "belowLo"):           false,
			belowGen(r[1]).Draw(t, "belowHi"):           true,
			equalGen(r[1]).Draw(t, "hi"):                false,
			equalGen(r[1]).Draw(t, "hiPost") + ".post0": false,
			equalGen(r[0]).Draw(t, "loPost") + ".post7": true,
		} {
			if got := tool.inRange(v); got != want {
				t.Fatalf("%s: inRange(%q) = %v, want %v for the supported range >= %s, < %s; outside it the adapter's output handling was never built against the tool", name, v, got, want, r[0], r[1])
			}
		}
	})
}

func nearBoundGen(lo, hi string) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		bound := rapid.SampledFrom([]string{lo, hi}).Draw(t, "bound")
		parts := strings.Split(bound, ".")
		parts = parts[:rapid.IntRange(1, len(parts)).Draw(t, "keep")]
		for i, p := range parts {
			n, _ := strconv.Atoi(p)
			n = max(0, n+rapid.IntRange(-1, 1).Draw(t, "delta"))
			parts[i] = strings.Repeat("0", rapid.IntRange(0, 2).Draw(t, "zeros")) + strconv.Itoa(n)
		}
		for range rapid.IntRange(0, 2).Draw(t, "extra") {
			parts = append(parts, rapid.SampledFrom([]string{"0", "1", "99999999999999999999"}).Draw(t, "seg"))
		}
		v := strings.Join(parts, ".")
		if rapid.Bool().Draw(t, "post") {
			v += ".post" + rapid.SampledFrom([]string{"0", "1", "99999999999999999999"}).Draw(t, "postN")
		}
		return v
	})
}

func TestRangeCheckIsTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom(specToolNames).Draw(t, "tool")
		tool := toolNamed(t, name)
		r := specRanges[name]
		v := rapid.OneOf(exactGen, nearBoundGen(r[0], r[1]), versionLikeGen).Draw(t, "version")
		if got, want := tool.inRange(v), specInRange(name, v); got != want {
			t.Fatalf("%s: inRange(%q) = %v, want %v; a version passes exactly when it is exact and >= %s, < %s, comparing release segments numerically with missing ones as zero and a post-release after its release", name, v, got, want, r[0], r[1])
		}
	})
}

// fakeVenv is a virtualenv as uv lays it out, holding only bin/python.
func fakeVenv(t fataler, dir string) {
	writeTree(t, dir, map[string]string{"bin/python": "#!/bin/sh\n", "lib/python3.13/site-packages/.keep": ""})
}

// uvDistDir is the .dist-info directory name uv writes for a distribution.
func uvDistDir(name, version string) string {
	return strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info"
}

func installDist(t fataler, venv, distDir, version string) {
	writeTree(t, venv, map[string]string{
		"lib/python3.13/site-packages/" + distDir + "/METADATA": "Metadata-Version: 2.4\nName: x\nVersion: " + version + "\nSummary: s\n\nVersion: 0.0.0\n",
	})
}

func installBins(t fataler, venv string, bins []string) {
	for _, b := range bins {
		writeTree(t, venv, map[string]string{"bin/" + b: "#!/bin/sh\n"})
	}
}

func installTool(t fataler, venv, name, version string) {
	installDist(t, venv, uvDistDir(name, version), version)
	if name == "cosmic-ray" {
		installBins(t, venv, cosmicRayBins)
	}
}

func installAllTools(t fataler, venv string, versions map[string]string) {
	fakeVenv(t, venv)
	for _, n := range specToolNames {
		v := versions[n]
		if v == "" {
			v = specRanges[n][0]
		}
		installTool(t, venv, n, v)
	}
}

func gitRepo(t *testing.T, files map[string]string, tracked ...string) string {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, files)
	git(t, top, "init", "-q")
	if len(tracked) > 0 {
		git(t, top, append([]string{"add", "--"}, tracked...)...)
	}
	return top
}

func uvRepo(t *testing.T) string {
	return gitRepo(t, map[string]string{"pyproject.toml": "", "uv.lock": ""}, "uv.lock")
}

func namedTools(msg string) map[string]bool {
	nameChar := func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	got := map[string]bool{}
	for _, tok := range strings.FieldsFunc(msg, func(r rune) bool { return !nameChar(r) }) {
		if _, ok := specRanges[strings.TrimRight(tok, ".")]; ok {
			got[strings.TrimRight(tok, ".")] = true
		}
	}
	return got
}

func mustMention(t fataler, msg string, words ...string) {
	for _, w := range words {
		if !strings.Contains(msg, w) {
			t.Fatalf("message %q does not mention %q", msg, w)
		}
	}
}

func envOf(t *testing.T, top, unit string) environment {
	env, problem, err := resolveEnvironment(top, unit)
	if err != nil || problem != "" {
		t.Fatalf("resolveEnvironment(%s): %q %v", unit, problem, err)
	}
	return env
}

func resolvedVersions(rs []resolved) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		out[r.name] = r.version
	}
	return out
}

func TestRelocatedVirtualenv(t *testing.T) {
	outside := realTempDir(t)
	for _, tc := range []struct{ env, venv string }{
		{"build/venv", "build/venv"},
		{filepath.Join(outside, "v"), ""},
	} {
		top := gitRepo(t, map[string]string{"svc/pyproject.toml": "", "uv.lock": ""}, "uv.lock")
		svc := filepath.Join(top, "svc")
		venv := tc.env
		if tc.venv != "" {
			venv = filepath.Join(top, tc.venv)
		}
		installAllTools(t, venv, map[string]string{"pytest": "9.0.7"})
		installAllTools(t, filepath.Join(svc, ".venv"), map[string]string{"pytest": "9.0.1"})
		installAllTools(t, filepath.Join(svc, "build/venv"), map[string]string{"pytest": "9.0.2"})
		t.Chdir(svc)
		t.Setenv("UV_PROJECT_ENVIRONMENT", tc.env)
		env := envOf(t, top, svc)
		rs, problem, err := resolveTools(top, env, []string{"pytest"})
		if err != nil || problem != "" || env.venv != venv || resolvedVersions(rs)["pytest"] != "9.0.7" {
			t.Fatalf("UV_PROJECT_ENVIRONMENT=%s: venv %s, tools %+v %q %v; want %s (pytest 9.0.7): a relative path is relative to the directory holding uv.lock, not the unit or the working directory, and the variable beats any .venv", tc.env, env.venv, rs, problem, err, venv)
		}
	}
}

func TestRelocatedVirtualenvMissing(t *testing.T) {
	for _, rel := range []string{"build/venv", filepath.Join(realTempDir(t), "gone")} {
		top := uvRepo(t)
		installAllTools(t, filepath.Join(top, ".venv"), nil)
		t.Setenv("UV_PROJECT_ENVIRONMENT", rel)
		_, problem, err := resolveEnvironment(top, top)
		if err != nil || problem == "" {
			t.Fatalf("UV_PROJECT_ENVIRONMENT=%s missing: problem %q, err %v; uv would create the environment there, so falling back to .venv tests against an install CI never builds", rel, problem, err)
		}
		mustMention(t, problem, rel, "UV_PROJECT_ENVIRONMENT", specInstall)
	}
}

func TestVirtualenvWithoutPython(t *testing.T) {
	top := uvRepo(t)
	installAllTools(t, filepath.Join(top, ".venv"), nil)
	if err := os.Rename(filepath.Join(top, ".venv/bin/python"), filepath.Join(top, ".venv/bin/python3")); err != nil {
		t.Fatal(err)
	}
	_, problem, err := resolveEnvironment(top, top)
	if err != nil || problem == "" {
		t.Fatalf("problem %q, err %v; a virtualenv with no bin/python cannot run pytest, so run must report environment rather than fail mid-run", problem, err)
	}
	mustMention(t, problem, ".venv", "bin/python", specInstall)
}

var levels = []string{"", "a", "a/b", "a/b/c"}

func pick(t *rapid.T, label string) map[string]bool {
	out := map[string]bool{}
	for _, l := range levels {
		if rapid.Bool().Draw(t, label+"@"+l) {
			out[l] = true
		}
	}
	return out
}

func nearest(set map[string]bool, unit string) (string, bool) {
	for i := slices.Index(levels, unit); i >= 0; i-- {
		if set[levels[i]] {
			return levels[i], true
		}
	}
	return "", false
}

type walkCase struct {
	top, unit, foreign, foreignAt string
	locks, venvs                  map[string]bool
	tracked                       bool
}

func drawWalkCase(t *rapid.T, repo string) walkCase {
	top, err := os.MkdirTemp(repo, "r")
	if err != nil {
		t.Fatalf("%v", err)
	}
	c := walkCase{
		top:       top,
		unit:      rapid.SampledFrom(levels).Draw(t, "unit"),
		locks:     pick(t, "lock"),
		venvs:     pick(t, "venv"),
		foreignAt: rapid.SampledFrom(append([]string{"none"}, levels...)).Draw(t, "foreignAt"),
		foreign:   rapid.SampledFrom(foreignLockNames).Draw(t, "foreign"),
		tracked:   rapid.Bool().Draw(t, "tracked"),
	}
	files := map[string]string{filepath.Join(c.unit, "pyproject.toml"): "", "a/b/c/d/uv.lock": "", "a/b/c/d/.venv/bin/python": ""}
	if c.foreignAt != "none" {
		files[filepath.Join(c.foreignAt, c.foreign)] = ""
	}
	for l := range pick(t, "req") {
		files[filepath.Join(l, rapid.SampledFrom([]string{"requirements.txt", "requirements-dev.txt"}).Draw(t, "req"))] = "pytest==10.0.0\n"
	}
	for l := range c.locks {
		files[filepath.Join(l, "uv.lock")] = ""
	}
	writeTree(t, top, files)
	for l := range c.venvs {
		fakeVenv(t, filepath.Join(top, l, ".venv"))
		installTool(t, filepath.Join(top, l, ".venv"), "pytest", "9.0."+strconv.Itoa(len(l)))
	}
	if lockLevel, ok := nearest(c.locks, c.unit); ok && c.tracked {
		git(t, top, "add", "--", filepath.Join(lockLevel, "uv.lock"))
	}
	return c
}

func checkWalkResolves(t *rapid.T, c walkCase, env environment, problem string) {
	lockLevel, _ := nearest(c.locks, c.unit)
	venvLevel, _ := nearest(c.venvs, c.unit)
	lockRel := filepath.ToSlash(filepath.Join(lockLevel, "uv.lock"))
	wantVenv := filepath.Join(c.top, venvLevel, ".venv")
	if problem != "" || env.venv != wantVenv || env.lock.rel != lockRel || env.lock.untracked == c.tracked || env.lock.dir != filepath.Join(c.top, lockLevel) {
		t.Fatalf("unit %q: env %+v, problem %q; want venv %s, lockfile %s relative to the top, untracked %v: the nearest .venv and uv.lock win independently, and requirements*.txt and non-adjacent foreign lockfiles are ignored", c.unit, env, problem, wantVenv, lockRel, !c.tracked)
	}
	rs, problem, err := resolveTools(c.top, env, []string{"pytest"})
	if err != nil || problem != "" || len(rs) != 1 || rs[0].version != "9.0."+strconv.Itoa(len(venvLevel)) || rs[0].pinnedBy != lockRel || rs[0].untracked == c.tracked {
		t.Fatalf("resolveTools from %s: %+v %q %v; pytest comes from the chosen .venv, pinned by %s, untracked %v", wantVenv, rs, problem, err, lockRel, !c.tracked)
	}
}

// TestEnvironmentWalks draws uv.lock, .venv, foreign lockfiles, and exported
// requirements at every level from a top directory down to a unit, plus
// decoys above the top and below the unit, and checks the outcome against the
// spec's walk rules.
func TestEnvironmentWalks(t *testing.T) {
	repo := gitRepo(t, map[string]string{"uv.lock": "", ".venv/bin/python": ""}, "uv.lock")
	installTool(t, filepath.Join(repo, ".venv"), "pytest", "9.9.9")
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	rapid.Check(t, func(t *rapid.T) {
		c := drawWalkCase(t, repo)
		env, problem, err := resolveEnvironment(c.top, filepath.Join(c.top, c.unit))
		if err != nil {
			t.Fatalf("resolveEnvironment: %v", err)
		}
		lockLevel, hasLock := nearest(c.locks, c.unit)
		_, hasVenv := nearest(c.venvs, c.unit)
		var words []string
		switch {
		case !hasLock:
			words = []string{"uv.lock"}
		case c.foreignAt == lockLevel:
			words = []string{filepath.ToSlash(filepath.Join(c.foreignAt, c.foreign))}
		case !hasVenv:
			words = []string{".venv", specInstall}
		default:
			checkWalkResolves(t, c, env, problem)
			return
		}
		if problem == "" {
			t.Fatalf("unit %q, uv.lock at %v, .venv at %v, %s at %q: env %+v and no problem; want one mentioning %q, because a uv.lock or .venv above the top or below the unit belongs to another project, and a foreign lockfile beside uv.lock leaves CI unable to tell which install to reproduce", c.unit, c.locks, c.venvs, c.foreign, c.foreignAt, env, words)
		}
		mustMention(t, problem, words...)
		if strings.Contains(problem, "requirements") {
			t.Fatalf("problem %q mentions requirements files, which the spec ignores", problem)
		}
	})
}

var foreignLockNames = []string{"poetry.lock", "pdm.lock", "Pipfile.lock"}

func TestExportedRequirementsIgnored(t *testing.T) {
	top := gitRepo(t, map[string]string{"pyproject.toml": "", "uv.lock": "", "requirements.txt": "pytest==10.0.0\n"}, "uv.lock", "requirements.txt")
	installAllTools(t, filepath.Join(top, ".venv"), nil)
	env := envOf(t, top, top)
	rs, problem, err := resolveTools(top, env, specToolNames)
	if err != nil || problem != "" || len(rs) != 7 || env.lock.rel != "uv.lock" || strings.Contains(problem, "requirements") {
		t.Fatalf("got %+v %q %v pinned by %q; requirements.txt is an export, so uv.lock pins and nothing reports on the export", rs, problem, err, env.lock.rel)
	}
}

func nameVariant(t *rapid.T, name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '-' {
			b.WriteString(rapid.SampledFrom([]string{"-", "_", ".", "__", "-_", "._-"}).Draw(t, "sep"))
			continue
		}
		s := string(r)
		if rapid.Bool().Draw(t, "upper") {
			s = strings.ToUpper(s)
		}
		b.WriteString(s)
	}
	return b.String()
}

func TestNormalizedDistributionName(t *testing.T) {
	top := uvRepo(t)
	env := environment{lock: lockfile{dir: top, rel: "uv.lock"}}
	rapid.Check(t, func(t *rapid.T) {
		venv, err := os.MkdirTemp(top, "v")
		if err != nil {
			t.Fatalf("%v", err)
		}
		fakeVenv(t, venv)
		env.venv = venv
		name := rapid.SampledFrom(specToolNames).Draw(t, "tool")
		version := specRanges[name][0]
		variant := nameVariant(t, name)
		installDist(t, venv, variant+"-"+version+".dist-info", version)
		if name == "cosmic-ray" {
			installBins(t, venv, cosmicRayBins)
		}
		decoy := rapid.SampledFrom([]string{name + "x", "x" + name, name + "-extra", "py" + name}).Draw(t, "decoy")
		installDist(t, venv, strings.ReplaceAll(decoy, "-", "_")+"-1.0.0.dist-info", "1.0.0")
		writeTree(t, venv, map[string]string{
			"lib/python3.13/site-packages/" + uvDistDir(name, "0.0.1") + "-not/METADATA": "Version: 0.0.1\n",
			"lib/python3.13/site-packages/" + uvDistDir(name, "0.0.2"):                   "",
		})

		rs, problem, err := resolveTools(top, env, []string{name})
		if err != nil || problem != "" || len(rs) != 1 || rs[0].version != version {
			t.Fatalf("%s installed as %s: got %+v %q %v; distribution names match after PEP 503 normalization (case, runs of -_. ), and %s, a non-.dist-info directory, or a .dist-info file is not an install of it", name, variant, rs, problem, err, decoy)
		}
	})
}

type installState int

const (
	stateOK installState = iota
	stateAbsent
	stateOutOfRange
	stateInexact
	stateDuplicate
	stateNoMetadata
	stateNoVersion
	stateMissingBin
)

var stateGen = rapid.SampledFrom([]installState{stateOK, stateOK, stateAbsent, stateOutOfRange, stateInexact, stateDuplicate, stateNoMetadata, stateNoVersion, stateMissingBin})

func inRangeGen(name string) *rapid.Generator[string] {
	r := specRanges[name]
	return rapid.OneOf(rapid.Just(r[0]), belowGen(r[1]), equalGen(r[0]))
}

// drawInstall installs one tool in the state drawn and reports the version a
// tools listing shows, and whether resolution must fail.
func drawInstall(t *rapid.T, venv, name string) (version string, fails bool) {
	st := stateGen.Draw(t, name)
	if st == stateMissingBin && name != "cosmic-ray" {
		st = stateOK
	}
	site := "lib/python3.13/site-packages/"
	switch st {
	case stateAbsent:
		return "", false
	case stateOutOfRange:
		r := specRanges[name]
		v := rapid.OneOf(belowGen(r[0]), equalGen(r[1])).Draw(t, name+"-out")
		installTool(t, venv, name, v)
		return v, true
	case stateInexact:
		v := specRanges[name][0] + rapid.SampledFrom([]string{"rc1", ".dev0", "+local", "a1"}).Draw(t, name+"-suffix")
		installTool(t, venv, name, v)
		return v, true
	case stateDuplicate:
		installTool(t, venv, name, specRanges[name][0])
		installTool(t, venv, name, belowGen(specRanges[name][1]).Draw(t, name+"-second"))
		return "", true
	case stateNoMetadata:
		writeTree(t, venv, map[string]string{site + uvDistDir(name, specRanges[name][0]) + "/RECORD": ""})
		return "", true
	case stateNoVersion:
		writeTree(t, venv, map[string]string{site + uvDistDir(name, specRanges[name][0]) + "/METADATA": "Name: " + name + "\n\nVersion: " + specRanges[name][0] + "\n"})
		return "", true
	case stateMissingBin:
		installTool(t, venv, name, specRanges[name][0])
		bin := rapid.SampledFrom(cosmicRayBins).Draw(t, "missingBin")
		if err := os.Rename(filepath.Join(venv, "bin", bin), filepath.Join(venv, "bin", bin+".bak")); err != nil {
			t.Fatalf("%v", err)
		}
		return "", true
	}
	v := inRangeGen(name).Draw(t, name+"-ok")
	installTool(t, venv, name, v)
	return v, false
}

func TestObjectiveToolSets(t *testing.T) {
	for obj, want := range specObjectiveTools {
		if got := objectiveTools[obj]; !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
			t.Errorf("%s resolves %q, want %q; an objective resolves exactly the tools it runs, so a missing unused tool never blocks it", obj, got, want)
		}
	}
	if len(objectiveTools) != len(specObjectiveTools) {
		t.Errorf("objectiveTools has %d objectives, want %d", len(objectiveTools), len(specObjectiveTools))
	}
}

// TestResolutionFollowsObjective draws an install state per tool and checks
// that each objective fails exactly on the failing tools it uses, naming each
// of them and the install line, and otherwise resolves exactly its tools.
func TestResolutionFollowsObjective(t *testing.T) {
	top := uvRepo(t)
	rapid.Check(t, func(t *rapid.T) {
		venv, err := os.MkdirTemp(top, "v")
		if err != nil {
			t.Fatalf("%v", err)
		}
		fakeVenv(t, venv)
		versions, failing := map[string]string{}, map[string]bool{}
		for _, n := range specToolNames {
			v, fails := drawInstall(t, venv, n)
			versions[n] = v
			if fails || v == "" {
				failing[n] = true
			}
		}
		obj := rapid.SampledFrom(slices.Sorted(maps.Keys(specObjectiveTools))).Draw(t, "objective")
		env := environment{lock: lockfile{dir: top, rel: "uv.lock"}, venv: venv}
		rs, problem, err := resolveTools(top, env, objectiveTools[obj])
		if err != nil {
			t.Fatalf("%v", err)
		}
		wantFail, wantOK := map[string]bool{}, map[string]string{}
		for _, n := range specObjectiveTools[obj] {
			if failing[n] {
				wantFail[n] = true
			} else {
				wantOK[n] = versions[n]
			}
		}
		if len(wantFail) > 0 {
			if rs != nil || !maps.Equal(namedTools(problem), wantFail) {
				t.Fatalf("%s: tools %+v, problem %q; want a problem naming exactly %v: every failing tool the objective uses, none it does not", obj, rs, problem, slices.Sorted(maps.Keys(wantFail)))
			}
			mustMention(t, problem, specInstall)
			return
		}
		if problem != "" || !maps.Equal(resolvedVersions(rs), wantOK) {
			t.Fatalf("%s: got %+v, problem %q; want exactly %v resolved, because tools the objective does not run are never checked", obj, rs, problem, wantOK)
		}
		checkResolvedFrom(t, rs, venv)
	})
}

func checkResolvedFrom(t fataler, rs []resolved, venv string) {
	for _, r := range rs {
		if r.pinnedBy != "uv.lock" {
			t.Fatalf("%s pinned by %q, want uv.lock", r.name, r.pinnedBy)
		}
		if r.name != "cosmic-ray" {
			continue
		}
		for _, b := range cosmicRayBins {
			if r.bins[b] != filepath.Join(venv, "bin", b) {
				t.Fatalf("cosmic-ray bins %v; %s must resolve from the virtualenv's bin, since the mutation run execs it there", r.bins, b)
			}
		}
	}
}

func TestToolNotInstalled(t *testing.T) {
	top := uvRepo(t)
	installAllTools(t, filepath.Join(top, ".venv"), nil)
	if err := os.RemoveAll(filepath.Join(top, ".venv/lib/python3.13/site-packages", uvDistDir("cosmic-ray", "8.7.0"))); err != nil {
		t.Fatal(err)
	}
	env := envOf(t, top, top)
	rs, problem, err := resolveTools(top, env, objectiveTools["VER-MUTATION-CHANGED"])
	if err != nil || rs != nil || !maps.Equal(namedTools(problem), map[string]bool{"cosmic-ray": true}) {
		t.Fatalf("got %+v %q %v; the mutation run needs cosmic-ray, so it must report environment naming it before running anything", rs, problem, err)
	}
	mustMention(t, problem, "not installed", specInstall)

	rs, problem, err = resolveTools(top, env, objectiveTools["VER-TESTS-PASS"])
	if err != nil || problem != "" || len(rs) != 3 {
		t.Fatalf("VER-TESTS-PASS with cosmic-ray missing: %+v %q %v; the test run never uses cosmic-ray, so its absence must not block test evidence", rs, problem, err)
	}
}

func TestTwoInstallsNamed(t *testing.T) {
	top := uvRepo(t)
	venv := filepath.Join(top, ".venv")
	installAllTools(t, venv, map[string]string{"pytest": "9.0.2"})
	installDist(t, venv, "pytest-9.0.3.dist-info", "9.0.3")
	_, problem, err := resolveTools(top, envOf(t, top, top), []string{"pytest"})
	if err != nil {
		t.Fatal(err)
	}
	mustMention(t, problem, "pytest-9.0.2.dist-info", "pytest-9.0.3.dist-info")
}

func TestOutOfRangeAndInexactNamed(t *testing.T) {
	for v, words := range map[string][]string{
		"10.0.0":   {"pytest", "10.0.0", ">= 9.0.0, < 10.0.0"},
		"9.1.0rc1": {"pytest", "9.1.0rc1"},
	} {
		top := uvRepo(t)
		installAllTools(t, filepath.Join(top, ".venv"), map[string]string{"pytest": v})
		_, problem, err := resolveTools(top, envOf(t, top, top), objectiveTools["VER-TESTS-PASS"])
		if err != nil || problem == "" {
			t.Fatalf("pytest %s: problem %q %v; resolution fails closed outside the exact supported versions", v, problem, err)
		}
		mustMention(t, problem, words...)
	}
}

func TestSitePackagesNotUnique(t *testing.T) {
	top := uvRepo(t)
	venv := filepath.Join(top, ".venv")
	installAllTools(t, venv, nil)
	writeTree(t, venv, map[string]string{"lib/python3.12/site-packages/.keep": ""})
	_, problem, err := resolveTools(top, envOf(t, top, top), []string{"pytest"})
	if err != nil || problem == "" {
		t.Fatalf("two site-packages: problem %q %v; with two interpreters' packages the adapter cannot tell which install pytest runs from", problem, err)
	}
	mustMention(t, problem, "site-packages", specInstall)
}

type toolsReply struct {
	resp    toolsResponse
	entries []map[string]json.RawMessage
}

func toolsReplyOf(t fataler, dir string) toolsReply {
	_, raw := invoke(t, "tools", "--root", dir)
	if vs, err := schemas.Validate(schemas.AdapterTools, raw); err != nil || len(vs) > 0 {
		t.Fatalf("tools --root %s: response %s fails the protocol schema, so assure rejects it: %v %v", dir, raw, err, vs)
	}
	var r toolsReply
	var keys struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r.resp); err != nil {
		t.Fatalf("%v", err)
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("%v", err)
	}
	r.entries = keys.Tools
	if r.resp.Protocol != 2 {
		t.Fatalf("tools protocol %d in %s; assure reads tools responses as protocol 2", r.resp.Protocol, raw)
	}
	return r
}

func checkUntracked(t fataler, r toolsReply, untracked bool) {
	for _, e := range r.entries {
		u, has := e["untracked"]
		if has != untracked || (has && string(u) != "true") {
			t.Fatalf("entry %s has untracked %s (present %v), want present %v; every tool is pinned by the one uv.lock, so all carry untracked: true or none does", e["name"], u, has, untracked)
		}
	}
}

// checkListing asserts all seven tools sorted by name, each pinned by lock
// with the fixed install line, carrying exactly the versions in installed and
// no version key for the rest.
func checkListing(t fataler, r toolsReply, lock string, installed map[string]string, untracked bool) {
	if r.resp.Environment != nil {
		t.Fatalf("environment %q; a tool not installed is fixed by the install line, so the tools op lists it rather than failing, or a fresh checkout cannot bootstrap", r.resp.Environment.Message)
	}
	var want []toolEntry
	for _, n := range specToolNames {
		want = append(want, toolEntry{Name: n, Version: installed[n], PinnedBy: lock, Install: specInstall})
	}
	if !slices.Equal(r.resp.Tools, want) {
		t.Fatalf("got %+v\nwant %+v\nall seven tools sorted by name, with a version only where the distribution is installed", r.resp.Tools, want)
	}
	for i, e := range r.entries {
		_, has := e["version"]
		if name := want[i].Name; has != (installed[name] != "") {
			t.Fatalf("entry %d (%s) has version key = %v; the protocol marks a tool not installed by omitting version, and rejects an empty one", i, name, has)
		}
	}
	checkUntracked(t, r, untracked)
}

// withPath leaves only git, and uv when withUV, on PATH.
func withPath(t *testing.T, withUV bool) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := realTempDir(t)
	bins := map[string]string{"git": "#!/bin/sh\nexec " + gitBin + " \"$@\"\n"}
	if withUV {
		bins["uv"] = "#!/bin/sh\nexit 3\n"
	}
	writeTree(t, bin, bins)
	for f := range bins {
		if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
}

func allLower() map[string]string {
	out := map[string]string{}
	for _, n := range specToolNames {
		out[n] = specRanges[n][0]
	}
	return out
}

func TestToolsForUvProject(t *testing.T) {
	withPath(t, true)
	for _, tracked := range []bool{true, false} {
		top := gitRepo(t, map[string]string{"pyproject.toml": "", "uv.lock": ""})
		if tracked {
			git(t, top, "add", "uv.lock")
		}
		installAllTools(t, filepath.Join(top, ".venv"), nil)
		checkListing(t, toolsReplyOf(t, top), "uv.lock", allLower(), !tracked)
	}
}

func TestToolsFreshCheckout(t *testing.T) {
	withPath(t, true)
	top := uvRepo(t)
	checkListing(t, toolsReplyOf(t, top), "uv.lock", nil, false)

	installAllTools(t, filepath.Join(top, ".venv"), nil)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "build/venv")
	checkListing(t, toolsReplyOf(t, top), "uv.lock", nil, false)
}

func TestToolsLockfileAboveRoot(t *testing.T) {
	withPath(t, true)
	top := gitRepo(t, map[string]string{"pyproject.toml": "", "infra/pyproject.toml": "", "uv.lock": ""}, "uv.lock")
	installAllTools(t, filepath.Join(top, ".venv"), nil)
	r := toolsReplyOf(t, filepath.Join(top, "infra"))
	if r.resp.Environment == nil || len(r.resp.Tools) != 0 {
		t.Fatalf("got %+v; uv sync --locked runs in the root, so it must be where uv.lock is", r.resp)
	}
	mustMention(t, r.resp.Environment.Message, "uv.lock", "from .")
	if strings.Contains(r.resp.Environment.Message, top) {
		t.Fatalf("message %q contains the absolute temp path; paths are repo-relative", r.resp.Environment.Message)
	}
}

func TestToolsRootThroughSymlink(t *testing.T) {
	withPath(t, true)
	top := uvRepo(t)
	installAllTools(t, filepath.Join(top, ".venv"), nil)
	link := filepath.Join(realTempDir(t), "link")
	if err := os.Symlink(top, link); err != nil {
		t.Fatal(err)
	}
	r := toolsReplyOf(t, link)
	if r.resp.Environment != nil {
		t.Fatalf("tools --root through a symlink: environment %q; git reports the resolved top-level, so the root must be resolved too or the unit and lockfile are never found (macOS temp and home paths are symlinks)", r.resp.Environment.Message)
	}
	checkListing(t, r, "uv.lock", allLower(), false)
}

func TestToolsUvMissing(t *testing.T) {
	withPath(t, false)
	top := uvRepo(t)
	installAllTools(t, filepath.Join(top, ".venv"), nil)
	r := toolsReplyOf(t, top)
	if r.resp.Environment == nil {
		t.Fatalf("got %+v with uv off PATH; CI could not run the install line", r.resp)
	}
	mustMention(t, r.resp.Environment.Message, "uv", "PATH", "mise")
}

func TestToolsEnvironmentFailures(t *testing.T) {
	withPath(t, true)
	site := ".venv/lib/python3.13/site-packages/"
	hypothesisMeta := site + uvDistDir("hypothesis", "6.130.0") + "/METADATA"
	withTools := func(versions map[string]string) func(*testing.T, string) {
		return func(t *testing.T, top string) { installAllTools(t, filepath.Join(top, ".venv"), versions) }
	}
	cases := map[string]struct {
		files map[string]string
		venv  func(*testing.T, string)
		words []string
	}{
		"no uv.lock":    {map[string]string{"pyproject.toml": ""}, withTools(nil), []string{"uv.lock"}},
		"poetry beside": {map[string]string{"pyproject.toml": "", "uv.lock": "", "poetry.lock": ""}, withTools(nil), []string{"poetry.lock"}},
		"no pyproject":  {map[string]string{"uv.lock": ""}, withTools(nil), []string{"pyproject.toml"}},
		"no bin/python": {map[string]string{"pyproject.toml": "", "uv.lock": "", site + ".keep": ""}, func(*testing.T, string) {}, []string{"bin/python", specInstall}},
		"out of range":  {map[string]string{"pyproject.toml": "", "uv.lock": ""}, withTools(map[string]string{"pytest": "10.0.0"}), []string{"pytest", "10.0.0", ">= 9.0.0, < 10.0.0", specInstall}},
		"inexact parso": {map[string]string{"pyproject.toml": "", "uv.lock": ""}, withTools(map[string]string{"parso": "0.8.7.dev1"}), []string{"parso", "0.8.7.dev1"}},
		"no metadata": {map[string]string{"pyproject.toml": "", "uv.lock": ""}, func(t *testing.T, top string) {
			withTools(nil)(t, top)
			if err := os.Remove(filepath.Join(top, hypothesisMeta)); err != nil {
				t.Fatal(err)
			}
		}, []string{"hypothesis", "METADATA"}},
		"no version header": {map[string]string{"pyproject.toml": "", "uv.lock": ""}, func(t *testing.T, top string) {
			withTools(nil)(t, top)
			writeTree(t, top, map[string]string{hypothesisMeta: "Metadata-Version: 2.4\nName: hypothesis\n\nVersion: 6.130.0\n"})
		}, []string{"hypothesis", "METADATA", "Version"}},
	}
	for name, tc := range cases {
		top := gitRepo(t, tc.files)
		tc.venv(t, top)
		r := toolsReplyOf(t, top)
		if r.resp.Environment == nil || len(r.resp.Tools) != 0 {
			t.Fatalf("%s: got %+v; the tools op reports environment and lists no tools on any failure other than not installed", name, r.resp)
		}
		mustMention(t, r.resp.Environment.Message, tc.words...)
	}
}

// TestToolsInstallStates draws a state per tool and checks the tools op
// lists every tool when nothing fails (versions only where installed), and
// otherwise reports environment naming exactly the failing tools.
func TestToolsInstallStates(t *testing.T) {
	withPath(t, true)
	repo := gitRepo(t, nil)
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(repo, "r")
		if err != nil {
			t.Fatalf("%v", err)
		}
		writeTree(t, dir, map[string]string{"pyproject.toml": "", "uv.lock": ""})
		tracked := rapid.Bool().Draw(t, "tracked")
		if tracked {
			git(t, repo, "add", "--", filepath.Base(dir)+"/uv.lock")
		}
		venv := filepath.Join(dir, ".venv")
		fakeVenv(t, venv)
		installed, failing := map[string]string{}, map[string]bool{}
		for _, n := range specToolNames {
			v, fails := drawInstall(t, venv, n)
			if fails {
				failing[n] = true
			} else if v != "" {
				installed[n] = v
			}
		}
		r := toolsReplyOf(t, dir)
		if len(failing) == 0 {
			checkListing(t, r, filepath.Base(dir)+"/uv.lock", installed, !tracked)
			return
		}
		if r.resp.Environment == nil || !maps.Equal(namedTools(r.resp.Environment.Message), failing) {
			t.Fatalf("got %+v; want environment naming exactly %v, every failing tool and no tool that is merely not installed", r.resp, slices.Sorted(maps.Keys(failing)))
		}
	})
}

func TestToolsUsageAndRootErrors(t *testing.T) {
	for _, args := range [][]string{
		{"tools"}, {"tools", "--root"}, {"tools", "--root", ""}, {"tools", "--root", ".", "extra"}, {"tools", "--bogus", "."},
		{"tools", "--root", filepath.Join(t.TempDir(), "missing")},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code == 0 || errb.Len() == 0 || out.Len() != 0 {
			t.Errorf("%q exited %d, stdout %q, stderr %q; assure treats a zero exit as a valid tools response", args, code, out.String(), errb.String())
		}
	}
}

var commitIDGen = rapid.OneOf(rapid.StringMatching(`[0-9a-f]{40}`), rapid.StringMatching(`[0-9a-f]{64}`))

func checkSeed(t fataler, sha string) string {
	s, err := seedFrom(sha)
	if err != nil {
		t.Fatalf("seedFrom(%q): %v; every commit id git prints must give a seed", sha, err)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s || n < 1 || n > 2147483647 {
		t.Fatalf("seedFrom(%q) = %q; the seed must be a decimal in [1, 2147483647], the range pytest-randomly and Hypothesis both accept", sha, s)
	}
	if again, _ := seedFrom(sha); again != s {
		t.Fatalf("seedFrom(%q) gave %q then %q; reruns on one commit must order tests and generate inputs identically", sha, s, again)
	}
	return s
}

func TestSeedFromCommitID(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		checkSeed(t, commitIDGen.Draw(t, "sha"))
	})
	seen := map[string]bool{}
	for _, prefix := range []string{"0000000000000000", "000000007fffffff", "000000007ffffffe", "0000000080000000", "00000000fffffffe", "ffffffffffffffff", "7fffffffffffffff", "0123456789abcdef"} {
		seen[checkSeed(t, prefix+strings.Repeat("0", 24))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("eight different commits all got seed %v; a seed that ignores the commit explores the same inputs forever", seen)
	}
}

func TestSeedFromRejectsNonCommitID(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sha := rapid.OneOf(
			rapid.StringMatching(`[0-9a-f]{0,15}`),
			rapid.Custom(func(t *rapid.T) string {
				id := []byte(commitIDGen.Draw(t, "sha"))
				id[rapid.IntRange(0, 15).Draw(t, "at")] = rapid.SampledFrom([]byte("gxz-+_ .\n")).Draw(t, "bad")
				return string(id)
			}),
		).Draw(t, "input")
		if s, err := seedFrom(sha); err == nil {
			t.Fatalf("seedFrom(%q) = %q, nil; output that is not a commit id must fail loudly rather than seed from garbage", sha, s)
		}
	})
}

func TestSeedFromLengthBoundary(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		id := rapid.StringMatching(`[0-9a-f]{16}`).Draw(t, "id")
		checkSeed(t, id)
		if s, err := seedFrom(id[:15]); err == nil {
			t.Fatalf("seedFrom(%q) = %q, nil; the seed reads 16 hex digits, so 15 must fail rather than seed from a short read", id[:15], s)
		}
	})
}

func TestSeedArgsSeedBothPlugins(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seed := checkSeed(t, commitIDGen.Draw(t, "sha"))
		args := seedArgs(seed)
		for _, plugin := range []string{"randomly", "hypothesispytest"} {
			if i := slices.Index(args, plugin); i < 1 || args[i-1] != "-p" {
				t.Fatalf("seedArgs(%s) = %q, missing -p %s; D3 loads both seeded plugins explicitly, so with PYTEST_DISABLE_PLUGIN_AUTOLOAD the order stays shuffled and pytest still accepts --hypothesis-seed", seed, args, plugin)
			}
		}
		for _, flag := range []string{"--randomly-seed=" + seed, "--hypothesis-seed=" + seed} {
			if !slices.Contains(args, flag) {
				t.Fatalf("seedArgs(%s) = %q, missing %s; test order and Hypothesis inputs must both follow the commit's seed", seed, args, flag)
			}
		}
		if !slices.Contains(args, "--hypothesis-profile=default") {
			t.Fatalf("seedArgs(%s) = %q, missing --hypothesis-profile=default; with CI or GITHUB_ACTIONS set, Hypothesis auto-loads its ci profile (derandomize=True), which ignores --hypothesis-seed, so draws would stop following the commit's seed in CI", seed, args)
		}
		if len(args) != 7 {
			t.Fatalf("seedArgs(%s) = %q; want only the two plugins, the two seed flags, and the default Hypothesis profile", seed, args)
		}
	})
}

func headOf(t *testing.T, dir string) string {
	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestAssureSeedFollowsHead(t *testing.T) {
	top := gitRepo(t, map[string]string{"a.py": ""})
	if s, err := assureSeed(top); err == nil {
		t.Fatalf("assureSeed with no commit = %q, nil; there is no HEAD to derive a seed from", s)
	}
	for i := range 2 {
		writeTree(t, top, map[string]string{"a.py": fmt.Sprint(i)})
		git(t, top, "add", ".")
		git(t, top, "commit", "-q", "-m", fmt.Sprint(i))
		first, err := assureSeed(top)
		if err != nil {
			t.Fatal(err)
		}
		second, err := assureSeed(top)
		if err != nil || first != second {
			t.Fatalf("assureSeed twice on one HEAD: %q then %q (%v)", first, second, err)
		}
		if want := checkSeed(t, headOf(t, top)); first != want {
			t.Fatalf("assureSeed = %q, want seedFrom(HEAD) = %q; the seed derives only from HEAD", first, want)
		}
	}
}
