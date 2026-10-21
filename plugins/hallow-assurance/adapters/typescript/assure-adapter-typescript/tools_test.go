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
	"vitest":                         {"5.0.0", "6.0.0"},
	"@vitest/coverage-v8":            {"5.0.0", "6.0.0"},
	"@stryker-mutator/core":          {"10.0.0", "11.0.0"},
	"@stryker-mutator/vitest-runner": {"10.0.0", "11.0.0"},
	"fast-check":                     {"4.0.0", "5.0.0"},
}

var specToolNames = slices.Sorted(maps.Keys(specRanges))

func specExact(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for i := range len(p) {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

func semverCompare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := new(big.Int).SetString(as[i], 10)
		y, _ := new(big.Int).SetString(bs[i], 10)
		if c := x.Cmp(y); c != 0 {
			return c
		}
	}
	return 0
}

func specInRange(name, v string) bool {
	r := specRanges[name]
	return specExact(v) && semverCompare(v, r[0]) >= 0 && semverCompare(v, r[1]) < 0
}

func toolNamed(t fataler, name string) tsTool {
	for _, tool := range tsTools {
		if tool.name == name {
			return tool
		}
	}
	t.Fatalf("no resolved tool %q; the spec lists it among the five", name)
	return tsTool{}
}

const (
	absentPkg = "\x00absent"
	badJSON   = "\x00badjson"
)

func installTool(t fataler, dir, name, version string) {
	data, err := json.Marshal(map[string]string{"name": name, "version": version})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if version == badJSON {
		data = []byte("{")
	}
	writeTree(t, dir, map[string]string{"node_modules/" + name + "/package.json": string(data)})
}

func installBin(t fataler, dir string, tool tsTool) {
	if tool.bin != "" {
		writeTree(t, dir, map[string]string{"node_modules/.bin/" + tool.bin: "#!/bin/sh\n"})
	}
}

func installAll(t fataler, dir string) {
	for _, name := range specToolNames {
		installTool(t, dir, name, specRanges[name][0])
		installBin(t, dir, toolNamed(t, name))
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

// namedTools reads tool names as whole tokens, so "vitest" inside
// "@vitest/coverage-v8" does not count.
func namedTools(msg string) map[string]bool {
	nameChar := func(r rune) bool {
		return r == '@' || r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	got := map[string]bool{}
	for _, tok := range strings.FieldsFunc(msg, func(r rune) bool { return !nameChar(r) }) {
		if _, ok := specRanges[strings.TrimRight(tok, ".")]; ok {
			got[strings.TrimRight(tok, ".")] = true
		}
	}
	return got
}

func mustMention(t fataler, msg, top string, words ...string) {
	if strings.Contains(msg, top) {
		t.Fatalf("message %q contains the absolute temp path %s; paths must be repo-relative, and the assertion below would false-pass on it", msg, top)
	}
	for _, w := range words {
		if !strings.Contains(msg, w) {
			t.Fatalf("message %q does not mention %q", msg, w)
		}
	}
}

var numGen = rapid.OneOf(
	rapid.Custom(func(t *rapid.T) string { return strconv.Itoa(rapid.IntRange(0, 12).Draw(t, "n")) }),
	rapid.StringMatching(`0{1,3}[0-9]{0,2}`),
	rapid.StringMatching(`[1-9][0-9]{15,30}`),
)

var exactGen = rapid.Custom(func(t *rapid.T) string {
	return numGen.Draw(t, "major") + "." + numGen.Draw(t, "minor") + "." + numGen.Draw(t, "patch")
})

var versionLikeGen = rapid.OneOf(
	exactGen,
	rapid.Custom(func(t *rapid.T) string {
		return exactGen.Draw(t, "v") + rapid.SampledFrom([]string{"-beta.1", "-rc.0", "-0", "+build.5", "\n", " ", ".0", "."}).Draw(t, "suffix")
	}),
	rapid.Custom(func(t *rapid.T) string {
		return rapid.SampledFrom([]string{"v", "=", "^", "~", " ", ">=", "\n"}).Draw(t, "prefix") + exactGen.Draw(t, "v")
	}),
	rapid.StringMatching(`[0-9.\-+a-z]{0,10}`),
	rapid.String(),
	rapid.SampledFrom([]string{"", "1.2", "1..2", ".1.2", "1.2.", "1.2.3.4", "١.٢.٣", "１.2.3", "1.2.٣", "latest", "workspace:*", "*", "1.x.0"}),
)

func TestExactVersionIsTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := versionLikeGen.Draw(t, "version")
		if got, want := isExact(v), specExact(v); got != want {
			t.Fatalf("isExact(%q) = %v, want %v; only MAJOR.MINOR.PATCH in ASCII digits is exact, because a range or prerelease names no single install CI can reproduce", v, got, want)
		}
	})
}

// justBelow is the greatest version that sorts below v in practice: the last
// non-zero component minus one, with every later component huge.
func justBelow(v string) string {
	parts := strings.Split(v, ".")
	for i := 2; i >= 0; i-- {
		n, _ := new(big.Int).SetString(parts[i], 10)
		if n.Sign() > 0 {
			parts[i] = n.Sub(n, big.NewInt(1)).String()
			for j := i + 1; j < 3; j++ {
				parts[j] = "99999999999999999999999"
			}
			return strings.Join(parts, ".")
		}
	}
	panic("no version below " + v)
}

func TestRangeBoundsPerTool(t *testing.T) {
	var names []string
	for _, tool := range tsTools {
		names = append(names, tool.name)
	}
	if slices.Sort(names); !slices.Equal(names, specToolNames) {
		t.Fatalf("resolved tools %q, want %q; each objective's tool set and the tools op draw from this list", names, specToolNames)
	}
	for _, name := range specToolNames {
		tool, r := toolNamed(t, name), specRanges[name]
		for v, want := range map[string]bool{
			r[0]: true, justBelow(r[0]): false,
			justBelow(r[1]): true, r[1]: false,
		} {
			if got := tool.inRange(v); got != want {
				t.Errorf("%s: inRange(%q) = %v, want %v for the supported range >= %s, < %s; outside it the adapter's output parsers were never built against the tool's output", name, v, got, want, r[0], r[1])
			}
		}
	}
}

func nearBoundGen(lo, hi string) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		bound := rapid.SampledFrom([]string{lo, hi}).Draw(t, "bound")
		parts := strings.Split(bound, ".")
		for i, p := range parts {
			n, _ := strconv.Atoi(p)
			n = max(0, n+rapid.IntRange(-1, 1).Draw(t, "delta"))
			parts[i] = strings.Repeat("0", rapid.IntRange(0, 2).Draw(t, "zeros")) + strconv.Itoa(n)
		}
		return strings.Join(parts, ".")
	})
}

func TestRangeCheckIsTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom(specToolNames).Draw(t, "tool")
		tool := toolNamed(t, name)
		r := specRanges[name]
		v := rapid.OneOf(exactGen, nearBoundGen(r[0], r[1]), versionLikeGen).Draw(t, "version")
		if got, want := tool.inRange(v), specInRange(name, v); got != want {
			t.Fatalf("%s: inRange(%q) = %v, want %v; a version passes exactly when it is exact and >= %s, < %s by numeric semver order (leading zeros and numbers past 64 bits included)", name, v, got, want, r[0], r[1])
		}
	})
}

func TestHoistedInstallResolves(t *testing.T) {
	top := gitRepo(t, map[string]string{
		"package.json":              `{"workspaces":["packages/*"]}`,
		"package-lock.json":         `{"lockfileVersion":3,"packages":{"node_modules/vitest":{"version":"5.0.1"}}}`,
		"packages/api/package.json": "{}",
	}, "package-lock.json")
	installTool(t, top, "vitest", "5.0.4")
	installBin(t, top, toolNamed(t, "vitest"))

	rs, env, err := resolveTools(top, filepath.Join(top, "packages/api"), []string{"vitest"})
	if err != nil || env != "" {
		t.Fatalf("resolveTools: %v %q; a workspace hoists its dependencies to the top-level node_modules, and the unit must still find them", err, env)
	}
	want := []resolved{{name: "vitest", version: "5.0.4", pinnedBy: "package-lock.json", install: "npm ci", bin: filepath.Join(top, "node_modules/.bin/vitest")}}
	if !slices.Equal(rs, want) {
		t.Fatalf("got %+v\nwant %+v\nthe version is the installed package's, never the lockfile entry's, and pinned_by is the lockfile path relative to the git top-level", rs, want)
	}
}

func TestTwoLockfilesInOneDirectoryFail(t *testing.T) {
	top := gitRepo(t, map[string]string{
		"package-lock.json":              "{}",
		"packages/api/package.json":      "{}",
		"packages/api/package-lock.json": "{}",
		"packages/api/yarn.lock":         "",
	}, "package-lock.json", "packages/api/package-lock.json", "packages/api/yarn.lock")
	unit := filepath.Join(top, "packages/api")
	installTool(t, unit, "vitest", "5.0.4")
	installBin(t, unit, toolNamed(t, "vitest"))

	rs, env, err := resolveTools(top, unit, []string{"vitest"})
	if err != nil || rs != nil || env == "" {
		t.Fatalf("got %+v %q %v; with two lockfiles CI cannot tell which install to reproduce, and the tracked one higher up must not mask that", rs, env, err)
	}
	mustMention(t, env, top, "packages/api", "package-lock.json", "yarn.lock")
}

func TestNoLockfileFails(t *testing.T) {
	top := gitRepo(t, map[string]string{"packages/api/package.json": "{}"})
	unit := filepath.Join(top, "packages/api")
	installTool(t, unit, "vitest", "5.0.4")
	installBin(t, unit, toolNamed(t, "vitest"))

	rs, env, err := resolveTools(top, unit, []string{"vitest"})
	if err != nil || rs != nil || env == "" {
		t.Fatalf("got %+v %q %v; with no lockfile up to the git top-level CI could not reproduce the install", rs, env, err)
	}
	mustMention(t, env, top, "vitest")
}

func TestInstalledToolFailures(t *testing.T) {
	cases := []struct {
		name, version string
		bin           bool
		words         []string
		why           string
	}{
		{"vitest", absentPkg, true, []string{"vitest", "npm ci"}, "a missing package"},
		{"vitest", "5.0.4", false, []string{"vitest", "npm ci"}, "a package without its node_modules/.bin executable"},
		{"vitest", "5.0.4-beta.1", true, []string{"vitest", "5.0.4-beta.1", "npm ci"}, "a prerelease version"},
		{"vitest", badJSON, true, []string{"vitest", "npm ci"}, "an unreadable package.json"},
	}
	for _, c := range cases {
		top := gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, "package-lock.json")
		if c.version != absentPkg {
			installTool(t, top, c.name, c.version)
		}
		if c.bin {
			installBin(t, top, toolNamed(t, c.name))
		}
		rs, env, err := resolveTools(top, top, []string{c.name})
		if err != nil || rs != nil || env == "" {
			t.Fatalf("%s: got %+v %q %v; resolution must fail with an environment message, never run the tool", c.why, rs, env, err)
		}
		mustMention(t, env, top, c.words...)
	}
}

func TestUnusedToolNotChecked(t *testing.T) {
	top := gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, "package-lock.json")
	installTool(t, top, "vitest", "5.0.4")
	installBin(t, top, toolNamed(t, "vitest"))

	rs, env, err := resolveTools(top, top, objectiveTools["VER-TESTS-PASS"])
	if err != nil || env != "" || len(rs) != 1 || rs[0].name != "vitest" {
		t.Fatalf("got %+v %q %v; @stryker-mutator/core is missing but VER-TESTS-PASS does not use it, so the run must proceed", rs, env, err)
	}
}

func TestToolNotInstalled(t *testing.T) {
	top := gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, "package-lock.json")
	for _, n := range []string{"vitest", "@stryker-mutator/vitest-runner"} {
		installTool(t, top, n, specRanges[n][0])
		installBin(t, top, toolNamed(t, n))
	}
	rs, env, err := resolveTools(top, top, objectiveTools["VER-MUTATION-CHANGED"])
	if err != nil || rs != nil || env == "" {
		t.Fatalf("got %+v %q %v; mutation testing without @stryker-mutator/core must report environment and write no evidence", rs, env, err)
	}
	mustMention(t, env, top, "@stryker-mutator/core", "npm ci")
	if got := namedTools(env); !maps.Equal(got, map[string]bool{"@stryker-mutator/core": true}) {
		t.Fatalf("message %q names %v; only @stryker-mutator/core is missing", env, got)
	}
}

func versionFor(tool tsTool) *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(absentPkg),
		nearBoundGen(tool.lo, tool.hi),
		nearBoundGen(tool.lo, tool.hi),
		rapid.SampledFrom([]string{badJSON, "", tool.lo + "-rc.1", "^" + tool.lo, "v" + tool.lo}),
	)
}

func drawInstall(t *rapid.T, tool tsTool, unit, ws string) (resolved, bool) {
	unitV := versionFor(tool).Draw(t, "unitVersion")
	wsV := versionFor(tool).Draw(t, "wsVersion")
	for dir, v := range map[string]string{unit: unitV, ws: wsV} {
		if v != absentPkg {
			installTool(t, dir, tool.name, v)
		}
	}
	binAt := ""
	if tool.bin != "" {
		binAt = rapid.SampledFrom([]string{"", unit, ws}).Draw(t, "binAt")
		if binAt != "" {
			installBin(t, binAt, tool)
		}
	}
	v := unitV
	if v == absentPkg {
		v = wsV
	}
	if v == absentPkg || v == badJSON || !specInRange(tool.name, v) || (tool.bin != "" && binAt == "") {
		return resolved{}, false
	}
	r := resolved{name: tool.name, version: v, pinnedBy: "package-lock.json", install: "npm ci"}
	if binAt != "" {
		r.bin = filepath.Join(binAt, "node_modules/.bin", tool.bin)
	}
	return r, true
}

func TestResolutionUsesNearestInstall(t *testing.T) {
	top := gitRepo(t, map[string]string{"package-lock.json": "{}"}, "package-lock.json")
	objectives := slices.Sorted(maps.Keys(objectiveTools))
	rapid.Check(t, func(t *rapid.T) {
		ws, err := os.MkdirTemp(top, "ws")
		if err != nil {
			t.Fatalf("%v", err)
		}
		unit := filepath.Join(ws, "pkg")
		writeTree(t, unit, map[string]string{"package.json": "{}"})
		obj := rapid.SampledFrom(objectives).Draw(t, "objective")
		uses := objectiveTools[obj]

		want := map[string]resolved{}
		wantFail := map[string]bool{}
		for _, tool := range tsTools {
			r, ok := drawInstall(t, tool, unit, ws)
			switch {
			case !slices.Contains(uses, tool.name):
			case !ok:
				wantFail[tool.name] = true
			default:
				want[tool.name] = r
			}
		}

		rs, env, err := resolveTools(top, unit, uses)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if len(wantFail) > 0 {
			if rs != nil || !maps.Equal(namedTools(env), wantFail) || !strings.Contains(env, "npm ci") || strings.Contains(env, top) {
				t.Fatalf("%s: got %+v, message %q naming %v; want every failing tool it uses (%v) named with the install line, and no tool it does not use", obj, rs, env, namedTools(env), wantFail)
			}
			return
		}
		got := map[string]resolved{}
		for _, r := range rs {
			got[r.name] = r
		}
		if env != "" || !maps.Equal(got, want) {
			t.Fatalf("%s: got %+v %q\nwant %+v\neach tool resolves from the nearest node_modules holding its package, and its executable from the nearest node_modules/.bin, independently", obj, rs, env, want)
		}
	})
}

func toolsResponseOf(t fataler, dir string) toolsResponse {
	_, raw := invoke(t, "tools", "--root", dir)
	if vs, err := schemas.Validate(schemas.AdapterTools, raw); err != nil || len(vs) > 0 {
		t.Fatalf("tools --root %s: response %s fails the protocol schema, so assure rejects it: %v %v", dir, raw, err, vs)
	}
	var resp toolsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v", err)
	}
	protocolIs(t, "tools", resp.Protocol, raw)
	return resp
}

func TestToolsForNpmRepository(t *testing.T) {
	top := gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, "package-lock.json")
	installAll(t, top)
	resp := toolsResponseOf(t, top)
	var want []toolEntry
	for _, n := range specToolNames {
		want = append(want, toolEntry{Name: n, Version: specRanges[n][0], PinnedBy: "package-lock.json", Install: "npm ci"})
	}
	if resp.Environment != nil || !slices.Equal(resp.Tools, want) {
		t.Fatalf("got %+v\nwant %+v\nassure tools prints one install line per tool and records pinned_by; all five tools, sorted by name", resp, want)
	}
}

func TestToolsDependenciesNotInstalled(t *testing.T) {
	top := gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, "package-lock.json")
	checkListing(t, toolsReplyOf(t, top), "package-lock.json", nil, false)
}

func TestToolsLockfileAboveRoot(t *testing.T) {
	for _, lockDir := range []string{".", "packages"} {
		lock := strings.TrimPrefix(lockDir+"/package-lock.json", "./")
		top := gitRepo(t, map[string]string{"packages/api/package.json": "{}", lock: "{}"}, lock)
		installAll(t, filepath.Join(top, "packages/api"))
		resp := toolsResponseOf(t, filepath.Join(top, "packages/api"))
		if resp.Environment == nil || len(resp.Tools) != 0 {
			t.Fatalf("lockfile %s, root packages/api: got %+v; the install line runs in the root, so it must be where the lockfile is", lock, resp)
		}
		mustMention(t, resp.Environment.Message, top, lock, "from "+lockDir)
		if strings.Contains(resp.Environment.Message, "from packages/api") {
			t.Fatalf("message %q sends the user back to the root it was run from", resp.Environment.Message)
		}
	}
}

func TestToolsRootWithoutUsableUnit(t *testing.T) {
	for marker, words := range map[string][]string{
		"":         {"package.json"},
		".pnp.cjs": {".pnp.cjs", "unsupported"},
	} {
		files := map[string]string{"package-lock.json": "{}", "sub/x.ts": ""}
		if marker != "" {
			files["package.json"] = "{}"
			files[marker] = ""
		}
		top := gitRepo(t, files, "package-lock.json")
		resp := toolsResponseOf(t, filepath.Join(top, "sub"))
		if resp.Environment == nil || len(resp.Tools) != 0 {
			t.Fatalf("marker %q: got %+v; with no package.json or a Plug'n'Play layout there is no node_modules install to resolve", marker, resp)
		}
		mustMention(t, resp.Environment.Message, top, words...)
	}
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

func specInstall(name, content string) string {
	switch name {
	case "package-lock.json":
		return "npm ci"
	case "pnpm-lock.yaml":
		return "pnpm install --frozen-lockfile"
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if i < len(lines)-1 {
			l = strings.TrimSuffix(l, "\r")
		}
		if l == "__metadata:" {
			return "yarn install --immutable"
		}
	}
	return "yarn install --frozen-lockfile"
}

var fixedInstalls = []string{"npm ci", "pnpm install --frozen-lockfile", "yarn install --immutable", "yarn install --frozen-lockfile"}

var lockLineGen = rapid.OneOf(
	rapid.SampledFrom([]string{
		"__metadata:", "  __metadata:", "__metadata: ", "__metadata", "__metadata:x", "a\r__metadata:", "__metadata:\rb",
		"# yarn lockfile v1", `"vitest@^5.0.0":`, "  version: 5.0.4", "  version 5.0.4", "", "npm ci; rm -rf /",
	}),
	rapid.Custom(func(t *rapid.T) string {
		n := rapid.OneOf(rapid.IntRange(64<<10-3, 64<<10+3), rapid.IntRange(64<<10, 200<<10)).Draw(t, "long")
		tail := rapid.SampledFrom([]string{"", "__metadata:"}).Draw(t, "tail")
		return strings.Repeat("x", max(0, n-len(tail))) + tail
	}),
	rapid.StringMatching(`[ -~]{0,20}`),
)

var lockContentGen = rapid.Custom(func(t *rapid.T) string {
	var b strings.Builder
	lines := rapid.SliceOfN(lockLineGen, 0, 5).Draw(t, "lines")
	for i, l := range lines {
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteString(rapid.SampledFrom([]string{"\n", "\r\n"}).Draw(t, "sep"))
		}
	}
	b.WriteString(rapid.SampledFrom([]string{"", "\n", "\r\n"}).Draw(t, "end"))
	return b.String()
})

var lockNameGen = rapid.SampledFrom([]string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock"})

func TestInstallLineIsFixed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := lockNameGen.Draw(t, "name")
		content := lockContentGen.Draw(t, "content")
		got, err := installFor(name, strings.NewReader(content))
		if err != nil {
			t.Fatalf("installFor(%s): %v; a readable lockfile always has an install line", name, err)
		}
		if want := specInstall(name, content); got != want || !slices.Contains(fixedInstalls, got) {
			t.Fatalf("installFor(%s, %d bytes) = %q, want %q; the line is fixed per lockfile kind (yarn berry only when a line is exactly __metadata:), and no repository text may reach a line the user is told to run", name, len(content), got, want)
		}
	})
}

func TestToolsInstallLineFollowsLockfile(t *testing.T) {
	top := gitRepo(t, nil)
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(top, "r")
		if err != nil {
			t.Fatalf("%v", err)
		}
		name := lockNameGen.Draw(t, "name")
		content := lockContentGen.Draw(t, "content")
		tracked := rapid.Bool().Draw(t, "tracked")
		lock := filepath.Base(dir) + "/" + name
		writeTree(t, dir, map[string]string{"package.json": "{}", name: content})
		installAll(t, dir)
		if tracked {
			git(t, top, "add", "--", lock)
		}

		r := toolsReplyOf(t, dir)
		resp := r.resp
		want := specInstall(name, content)
		var names []string
		for _, e := range resp.Tools {
			names = append(names, e.Name)
			if e.Install != want || e.PinnedBy != lock {
				t.Fatalf("%s: entry %+v, want install %q pinned by %s", lock, e, want, lock)
			}
		}
		if resp.Environment != nil || !slices.Equal(names, specToolNames) {
			t.Fatalf("%s (tracked %v): got %+v; all five tools, sorted by name, whether or not git tracks the lockfile", lock, tracked, resp)
		}
		checkUntracked(t, r, !tracked)
	})
}

var commitIDGen = rapid.OneOf(rapid.StringMatching(`[0-9a-f]{40}`), rapid.StringMatching(`[0-9a-f]{64}`))

func checkSeed(t fataler, sha string) string {
	s, err := seedFrom(sha)
	if err != nil {
		t.Fatalf("seedFrom(%q): %v; every commit id git prints must give a seed", sha, err)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s || n < 1 || n > 2147483647 {
		t.Fatalf("seedFrom(%q) = %q; ASSURE_SEED must be a decimal in [1, 2147483647], the range fast-check accepts", sha, s)
	}
	if again, _ := seedFrom(sha); again != s {
		t.Fatalf("seedFrom(%q) gave %q then %q; reruns on one commit must generate the same inputs", sha, s, again)
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

func headOf(t *testing.T, dir string) string {
	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestAssureSeedFollowsHead(t *testing.T) {
	top := gitRepo(t, map[string]string{"a.ts": ""})
	if s, err := assureSeed(top); err == nil {
		t.Fatalf("assureSeed with no commit = %q, nil; there is no HEAD to derive a seed from", s)
	}
	for i := range 2 {
		writeTree(t, top, map[string]string{"a.ts": fmt.Sprint(i)})
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
