package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type fataler interface{ Fatalf(string, ...any) }

func invoke(t fataler, args ...string) (map[string]any, []byte) {
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, errb.String())
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return v, out.Bytes()
}

type entry struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Role     string `json:"role"`
}

func classifyAll(t fataler, paths ...string) []entry {
	_, raw := invoke(t, append([]string{"classify"}, paths...)...)
	if vs, err := schemas.Validate(schemas.AdapterClassify, raw); err != nil || len(vs) > 0 {
		t.Fatalf("classify %q: response fails the protocol schema, so assure rejects every classification from this adapter: %v %v", paths, err, vs)
	}
	var resp struct{ Files []entry }
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("classify %q: %v", paths, err)
	}
	return resp.Files
}

var specConfigNames = []string{"pyproject.toml", "uv.lock", "pytest.ini", "setup.cfg", "tox.ini", ".coveragerc"}

// specRole applies the spec's glob list with the protocol precedence
// (generated, fuzz_corpus, test, config; else source for a claimed path).
// Every spec glob is **/<name>, so only the base name decides.
func specRole(p string) (string, bool) {
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case strings.HasSuffix(base, ".pyi"):
		return "generated", true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.HasSuffix(base, "_test.py"), base == "conftest.py":
		return "test", true
	case slices.Contains(specConfigNames, base):
		return "config", true
	case strings.HasSuffix(base, ".py"):
		return "source", true
	}
	return "", false
}

var segGen = rapid.StringMatching(`[a-z]{1,5}`)

var nameGen = rapid.OneOf(
	rapid.Custom(func(t *rapid.T) string {
		prefix := rapid.SampledFrom([]string{"", "test_", "test", "_", "Test_"}).Draw(t, "prefix")
		stem := rapid.OneOf(segGen, rapid.SampledFrom([]string{"", "conftest", "pyproject", "uv", "pytest", "setup", "tox", "__init__", "test", "parse"})).Draw(t, "stem")
		mid := rapid.SampledFrom([]string{"", "_test", "_fuzz", ".test", "-test", "_tests"}).Draw(t, "mid")
		ext := rapid.SampledFrom([]string{"py", "pyi", "pyc", "pyw", "ipynb", "toml", "lock", "ini", "cfg", "txt", "coveragerc", "py.bak"}).Draw(t, "ext")
		return prefix + stem + mid + "." + ext
	}),
	rapid.SampledFrom([]string{
		".coveragerc", "pyproject.toml", "uv.lock", "pytest.ini", "setup.cfg", "tox.ini", "conftest.py",
		"conftest.pyi", "test_.py", "_test.py", ".py", ".pyi", "test_x.pyi", "requirements.txt",
		"setup.py", "analysis.ipynb", "poetry.lock", "Pyproject.toml", "pyproject.toml.bak",
	}),
)

var pathGen = rapid.Custom(func(t *rapid.T) string {
	dirs := rapid.SliceOfN(rapid.OneOf(segGen, rapid.SampledFrom([]string{"src", "tests", "test_x.py", "x.pyi", "pyproject.toml", "conftest.py", ".venv"})), 0, 3).Draw(t, "dirs")
	return strings.Join(append(dirs, nameGen.Draw(t, "name")), "/")
})

func TestDescribeConformsToProtocol(t *testing.T) {
	v, raw := invoke(t, "describe")
	if vs, err := schemas.Validate(schemas.AdapterDescribe, raw); err != nil || len(vs) > 0 {
		t.Fatalf("describe invalid: %v %v; assure refuses an adapter whose describe fails the schema", err, vs)
	}
	if l, _ := v["languages"].([]any); len(l) != 1 || l[0] != "python" {
		t.Fatalf("languages = %v; assure classify reports this as the file's language", v["languages"])
	}
	if v["reference"] != "python" {
		t.Fatalf("reference = %v; assure reference python cannot find this adapter's reference", v["reference"])
	}
	if v["protocol"] != float64(2) {
		t.Fatalf("protocol = %v; describe must announce protocol 2, the protocol without CODE-* objectives", v["protocol"])
	}
	var fast []string
	for id, o := range v["objectives"].(map[string]any) {
		if strings.HasPrefix(id, "CODE-") {
			t.Errorf("describe lists %s; code-shape objectives left the catalog", id)
		}
		if o.(map[string]any)["fast"] == true {
			fast = append(fast, id)
		}
	}
	if !slices.Equal(fast, []string{"VER-TESTS-PASS"}) {
		t.Fatalf("fast objectives %v; VER-TESTS-PASS is the whole Stop-path set, and another fast objective would run mutation or coverage on every Stop", fast)
	}
	checkDescribeGlobs(t, v)
}

func checkDescribeGlobs(t *testing.T, v map[string]any) {
	want := map[string][]string{
		"claims":    {"**/*.py", "**/*.pyi"},
		"generated": {"**/*.pyi"},
		"test":      {"**/*_test.py", "**/conftest.py", "**/test_*.py"},
		"config":    {"**/.coveragerc", "**/pyproject.toml", "**/pytest.ini", "**/setup.cfg", "**/tox.ini", "**/uv.lock"},
	}
	got := map[string][]string{"claims": sortedStrs(v["claims"])}
	for role, g := range v["patterns"].(map[string]any) {
		got[role] = sortedStrs(g)
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("globs = %q, want %q; assure core classifies changed files from these globs, so a drift silently moves files between objectives (fuzz_corpus must be absent)", got, want)
	}
	for _, globs := range got {
		for _, g := range globs {
			if strings.Contains(g, "{") {
				t.Errorf("glob %q uses a brace; core globs match braces literally, so the pattern would never match", g)
			}
		}
	}
}

func sortedStrs(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	slices.Sort(out)
	return out
}

func TestLintSubcommandIsAbsent(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"lint", "src"}, &out, &errb); code == 0 || out.Len() != 0 {
		t.Fatalf("lint exited %d with stdout %q; the spec forbids a lint subcommand, so it must fail as unknown", code, out.String())
	}
}

func TestClassifyNonexistentPathsFollowSpecGlobsInInputOrder(t *testing.T) {
	t.Chdir(t.TempDir())
	rapid.Check(t, func(t *rapid.T) {
		paths := rapid.SliceOfN(pathGen, 1, 6).Draw(t, "paths")
		var want []entry
		for _, p := range paths {
			if role, ok := specRole(p); ok {
				want = append(want, entry{Path: p, Language: "python", Role: role})
			}
		}
		if got := classifyAll(t, paths...); !slices.Equal(got, want) {
			t.Fatalf("classify %q\n got %v\nwant %v\na wrong role moves a file in or out of the objectives that guard it; a claimed .ipynb or a reordered entry misattributes evidence; a missing file must classify by its globs so deletions still reach their unit", paths, got, want)
		}
	})
}

var leadLineGen = rapid.SampledFrom([]string{
	"", "   ", "\t", "#", "# coding=utf-8", "#!/usr/bin/env python3", "  # indented comment",
	"  # Code generated by x. DO NOT EDIT.",
	"# Code generated by x. DO NOT EDIT",
	"# Code generated by x. DO NOT EDIT. ",
	"#Code generated by x. DO NOT EDIT.",
	"# *** WARNING: this file was generated by x. **",
	"# *** WARNING: this file was generated by x. *** ",
})

var codeLineGen = rapid.SampledFrom([]string{
	"import os", "from a import b", "// Code generated by x. DO NOT EDIT.", "x = 1", "def f(): pass", "  pass",
	`"""# Code generated by x. DO NOT EDIT."""`,
	"x = 1  # Code generated by x. DO NOT EDIT.",
	`"# *** WARNING: this file was generated by x. ***"`,
})

func headerLine(kind, tool string) string {
	if kind == "pulumi" {
		return "# *** WARNING: this file was generated by " + tool + " ***"
	}
	return "# Code generated " + tool + " DO NOT EDIT."
}

func TestGeneratedHeaderOnlyBeforeFirstCodeLine(t *testing.T) {
	t.Chdir(t.TempDir())
	n := 0
	rapid.Check(t, func(t *rapid.T) {
		name := pathGen.Filter(func(p string) bool { _, ok := specRole(p); return ok }).Draw(t, "name")
		before := rapid.SliceOfN(leadLineGen, 0, 3).Draw(t, "before")
		codeFirst := rapid.Bool().Draw(t, "codeFirst")
		withHeader := rapid.Bool().Draw(t, "withHeader")
		kind := rapid.SampledFrom([]string{"go", "pulumi"}).Draw(t, "kind")
		tool := rapid.StringMatching(`[ -~]{0,12}`).Draw(t, "tool")
		after := rapid.SliceOfN(rapid.OneOf(leadLineGen, codeLineGen), 0, 3).Draw(t, "after")

		lines := slices.Clone(before)
		if codeFirst {
			lines = append(lines, codeLineGen.Draw(t, "code"))
		}
		if withHeader {
			lines = append(lines, headerLine(kind, tool))
		}
		lines = append(lines, after...)
		src := strings.Join(lines, rapid.SampledFrom([]string{"\n", "\r\n"}).Draw(t, "eol"))
		if rapid.Bool().Draw(t, "trailingNewline") {
			src += "\n"
		}

		n++
		p := fmt.Sprintf("%d/%s", n, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("%v", err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("%v", err)
		}

		want, _ := specRole(name)
		if withHeader && !codeFirst && strings.HasSuffix(name, ".py") {
			want = "generated"
		}
		got := classifyAll(t, p)
		if len(got) != 1 || got[0].Role != want {
			t.Fatalf("%s with content %q: got %v, want role %q; a header below code must not exempt hand-written code from mutation and coverage, and a header above it must exempt generated code", p, src, got, want)
		}
	})
}

func checkLongComment(t fataler, p string, size int, ending string) {
	src := "#" + strings.Repeat("x", size-1)
	want := "source"
	switch ending {
	case "header":
		src += "\n# Code generated x. DO NOT EDIT.\nimport os\n"
		want = "generated"
	case "newline-eof":
		src += "\n"
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"classify", p}, &out, &errb); code != 0 {
		t.Fatalf("%d-byte # line, ending %s: exited %d: %s; a readable file must classify, whatever its line lengths", size, ending, code, errb.String())
	}
	if got := classifyAll(t, p); len(got) != 1 || got[0].Role != want {
		t.Fatalf("%d-byte # line, ending %s: got %v, want %s; a long comment is still a comment, so a header on the next line marks the file generated", size, ending, got, want)
	}
}

func TestLongCommentLineBeforeHeader(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, size := range []int{64 << 10, 128 << 10} {
		checkLongComment(t, fmt.Sprintf("exact%d.py", size), size, "eof")
	}
	n := 0
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.OneOf(
			rapid.SampledFrom([]int{64<<10 - 1, 64 << 10, 64<<10 + 1, 128 << 10, 128<<10 + 1, 192 << 10}),
			rapid.IntRange(64<<10, 200<<10),
		).Draw(t, "size")
		ending := rapid.SampledFrom([]string{"header", "eof", "newline-eof"}).Draw(t, "ending")
		n++
		checkLongComment(t, fmt.Sprintf("big%d.py", n), size, ending)
	})
}

func TestUnknownSubcommandFails(t *testing.T) {
	for _, args := range [][]string{{"frobnicate"}, {}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code == 0 || errb.Len() == 0 || out.Len() != 0 {
			t.Errorf("%q exited %d with stdout %q stderr %q; assure treats a zero exit as a valid response and would parse empty stdout", args, code, out.String(), errb.String())
		}
	}
}

func TestUnreadablePyFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	t.Chdir(t.TempDir())
	if err := os.WriteFile("locked.py", []byte("import os\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"classify", "locked.py"}, &out, &errb); code == 0 || errb.Len() == 0 {
		t.Fatalf("unreadable locked.py exited %d with stderr %q; classifying it as source without reading its header could hide a generated file or misreport one", code, errb.String())
	}
}

func TestUnreadableNonPyClaimedFilesClassifiedByGlobs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	t.Chdir(t.TempDir())
	want := []entry{
		{Path: "pyproject.toml", Language: "python", Role: "config"},
		{Path: "env.pyi", Language: "python", Role: "generated"},
	}
	for _, e := range want {
		if err := os.WriteFile(e.Path, []byte("# Code generated x. DO NOT EDIT.\n"), 0o000); err != nil {
			t.Fatal(err)
		}
	}
	if got := classifyAll(t, "pyproject.toml", "env.pyi"); !slices.Equal(got, want) {
		t.Fatalf("mode-000 pyproject.toml and env.pyi: got %v, want %v; only .py files are opened for the header, so other claimed files classify by their globs alone", got, want)
	}
}

func TestReferencePrintsEmbeddedFile(t *testing.T) {
	want, err := os.ReadFile("reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(want) >= 256<<10 {
		t.Errorf("reference.md is %d bytes; the spec caps it under 256 KiB", len(want))
	}
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"reference"}, &out, &errb); code != 0 {
		t.Fatalf("reference exited %d: %s", code, errb.String())
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("reference printed %d bytes that differ from reference.md (%d bytes); assure reference python must show the shipped guidance from any directory", out.Len(), len(want))
	}
}
