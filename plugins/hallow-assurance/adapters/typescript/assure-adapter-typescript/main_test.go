package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

var (
	specSourceExts = []string{"ts", "tsx", "mts", "cts"}
	specConfigExts = []string{"ts", "mts", "cts", "js", "mjs", "cjs"}
)

func endsWithExt(base, infix string, exts []string) bool {
	for _, e := range exts {
		suf := infix + "." + e
		if len(base) > len(suf) && strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

func specConfig(base string) bool {
	switch base {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "tsconfig.json":
		return true
	}
	if strings.HasPrefix(base, "tsconfig.") && strings.HasSuffix(base, ".json") && len(base) >= len("tsconfig..json") {
		return true
	}
	for _, e := range specConfigExts {
		if base == "vitest.config."+e || base == "eslint.config."+e {
			return true
		}
	}
	return false
}

// specRole applies the spec's glob list with the protocol precedence
// (generated, fuzz_corpus, test, config; else source for a claimed path).
func specRole(p string) (string, bool) {
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case endsWithExt(base, ".d", []string{"ts", "mts", "cts"}):
		return "generated", true
	case endsWithExt(base, ".test", specSourceExts), endsWithExt(base, ".spec", specSourceExts):
		return "test", true
	case specConfig(base):
		return "config", true
	case endsWithExt(base, "", specSourceExts):
		return "source", true
	}
	return "", false
}

func specSourceExt(p string) bool {
	return endsWithExt(p[strings.LastIndex(p, "/")+1:], "", specSourceExts)
}

var segGen = rapid.StringMatching(`[a-z]{1,5}`)

var nameGen = rapid.Custom(func(t *rapid.T) string {
	stem := rapid.OneOf(segGen, rapid.SampledFrom([]string{"vitest", "eslint", "tsconfig", "package", "pnpm", "yarn", "index"})).Draw(t, "stem")
	mid := rapid.SampledFrom([]string{"", ".test", ".spec", ".fuzz.test", ".d", ".config", ".base", "-lock", ".config.test", ".d.test"}).Draw(t, "mid")
	ext := rapid.SampledFrom([]string{"ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs", "json", "yaml", "lock", "md"}).Draw(t, "ext")
	return stem + mid + "." + ext
})

var pathGen = rapid.Custom(func(t *rapid.T) string {
	dirs := rapid.SliceOfN(rapid.OneOf(segGen, rapid.SampledFrom([]string{"src", "packages", "node_modules", "x.ts", "tsconfig.json", "package.json"})), 0, 3).Draw(t, "dirs")
	return strings.Join(append(dirs, nameGen.Draw(t, "name")), "/")
})

func TestDescribeConformsToProtocol(t *testing.T) {
	v, raw := invoke(t, "describe")
	if vs, err := schemas.Validate(schemas.AdapterDescribe, raw); err != nil || len(vs) > 0 {
		t.Fatalf("describe invalid: %v %v; assure refuses an adapter whose describe fails the schema", err, vs)
	}
	if l, _ := v["languages"].([]any); len(l) != 1 || l[0] != "typescript" {
		t.Fatalf("languages = %v; assure classify reports this as the file's language", v["languages"])
	}
	if v["reference"] != "typescript" {
		t.Fatalf("reference = %v; assure reference typescript cannot find this adapter's reference", v["reference"])
	}
	globs := v["claims"].([]any)
	for _, g := range v["patterns"].(map[string]any) {
		globs = append(globs, g.([]any)...)
	}
	for _, g := range globs {
		if strings.Contains(g.(string), "{") {
			t.Errorf("glob %q uses a brace; core globs match braces literally, so the pattern would never match", g)
		}
	}
}

func TestClassifyFollowsSpecGlobsInInputOrder(t *testing.T) {
	t.Chdir(t.TempDir())
	rapid.Check(t, func(t *rapid.T) {
		paths := rapid.SliceOfN(pathGen, 1, 6).Draw(t, "paths")
		var want []entry
		for _, p := range paths {
			if role, ok := specRole(p); ok {
				want = append(want, entry{Path: p, Language: "typescript", Role: role})
			}
		}
		if got := classifyAll(t, paths...); !slices.Equal(got, want) {
			t.Fatalf("classify %q\n got %v\nwant %v\na wrong role moves a file in or out of the objectives that guard it; an unclaimed .js file or a reordered entry misattributes evidence", paths, got, want)
		}
	})
}

var leadLineGen = rapid.SampledFrom([]string{
	"", "   ", "\t", "//", "// eslint-disable", "  // indented comment",
	"  // Code generated by x. DO NOT EDIT.",
	"// Code generated by x. DO NOT EDIT",
	"// Code generated by x. DO NOT EDIT. ",
})

var codeLineGen = rapid.SampledFrom([]string{
	`import { a } from "./a";`, "export {};", `"use strict";`,
	"/* Code generated by x. DO NOT EDIT. */",
	"/*", "x // Code generated by x. DO NOT EDIT.",
})

func TestGeneratedHeaderOnlyBeforeFirstCodeLine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	n := 0
	rapid.Check(t, func(t *rapid.T) {
		name := pathGen.Filter(func(p string) bool { _, ok := specRole(p); return ok }).Draw(t, "name")
		before := rapid.SliceOfN(leadLineGen, 0, 3).Draw(t, "before")
		codeFirst := rapid.Bool().Draw(t, "codeFirst")
		withHeader := rapid.Bool().Draw(t, "withHeader")
		tool := rapid.StringMatching(`[ -~]{0,12}`).Draw(t, "tool")
		after := rapid.SliceOfN(rapid.OneOf(leadLineGen, codeLineGen), 0, 3).Draw(t, "after")

		lines := slices.Clone(before)
		if codeFirst {
			lines = append(lines, codeLineGen.Draw(t, "code"))
		}
		if withHeader {
			lines = append(lines, "// Code generated "+tool+" DO NOT EDIT.")
		}
		lines = append(lines, after...)
		src := strings.Join(lines, "\n")
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
		if withHeader && !codeFirst && specSourceExt(name) {
			want = "generated"
		}
		got := classifyAll(t, p)
		if len(got) != 1 || got[0].Role != want {
			t.Fatalf("%s with content %q: got %v, want role %q; a header below code must not exempt hand-written code from the source objectives, and a header above it must exempt generated code", p, src, got, want)
		}
	})
}

func checkLongComment(t fataler, p string, size int, ending string) {
	src := "//" + strings.Repeat("x", size-2)
	want := "source"
	switch ending {
	case "header":
		src += "\n// Code generated x. DO NOT EDIT.\nexport {};\n"
		want = "generated"
	case "newline-eof":
		src += "\n"
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"classify", p}, &out, &errb); code != 0 {
		t.Fatalf("%d-byte // line, ending %s: exited %d: %s; a readable file must classify, whatever its line lengths", size, ending, code, errb.String())
	}
	if got := classifyAll(t, p); len(got) != 1 || got[0].Role != want {
		t.Fatalf("%d-byte // line, ending %s: got %v, want %s; a long comment is still a comment, so a header on the next line marks the file generated", size, ending, got, want)
	}
}

func TestLongCommentLineBeforeHeader(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, size := range []int{64 << 10, 128 << 10} {
		checkLongComment(t, fmt.Sprintf("exact%d.ts", size), size, "eof")
	}
	n := 0
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.OneOf(
			rapid.SampledFrom([]int{64<<10 - 1, 64 << 10, 64<<10 + 1, 128 << 10, 128<<10 + 1, 192 << 10}),
			rapid.IntRange(64<<10, 200<<10),
		).Draw(t, "size")
		ending := rapid.SampledFrom([]string{"header", "eof", "newline-eof"}).Draw(t, "ending")
		n++
		checkLongComment(t, fmt.Sprintf("big%d.ts", n), size, ending)
	})
}

func TestUnknownSubcommandFails(t *testing.T) {
	for _, args := range [][]string{{"frobnicate"}, {}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code == 0 || errb.Len() == 0 {
			t.Errorf("%q exited %d with stderr %q; assure treats a zero exit as a valid response and would parse empty stdout", args, code, errb.String())
		}
	}
}

func TestUnreadableClaimedFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	t.Chdir(t.TempDir())
	if err := os.WriteFile("locked.ts", []byte("export {};\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"classify", "locked.ts"}, &out, &errb); code == 0 || errb.Len() == 0 {
		t.Fatalf("unreadable locked.ts exited %d with stderr %q; classifying it as source without reading its header could hide a generated file or misreport one", code, errb.String())
	}
}

func TestUnreadableConfigClassifiedByGlobs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	t.Chdir(t.TempDir())
	if err := os.WriteFile("package.json", []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	if got := classifyAll(t, "package.json"); len(got) != 1 || got[0].Role != "config" {
		t.Fatalf("mode-000 package.json: got %v, want config; only TypeScript sources are opened for the header, so other claimed files classify by their globs alone", got)
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
		t.Fatalf("reference printed %d bytes that differ from reference.md (%d bytes); assure reference typescript must show the shipped guidance from any directory", out.Len(), len(want))
	}
}
