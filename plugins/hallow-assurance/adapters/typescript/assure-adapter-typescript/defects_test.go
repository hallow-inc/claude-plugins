package main

import (
	"bytes"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLongFirstLineStillClassifies(t *testing.T) {
	t.Chdir(t.TempDir())
	src := `export const data = "` + strings.Repeat("x", 70<<10) + "\";\n"
	if err := os.WriteFile("data.ts", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"classify", "data.ts"}, &out, &errb); code != 0 {
		t.Fatalf("readable data.ts with a 70 KiB first line exited %d: %s; the spec allows a non-zero exit only for an unknown subcommand or an unreadable file, and one long embedded literal must not stop assure classifying the change", code, errb.String())
	}
	if got := classifyAll(t, "data.ts"); len(got) != 1 || got[0].Role != "source" {
		t.Fatalf("data.ts: got %v, want source", got)
	}
}

func TestConfigChangeSelectsNonASCIIFile(t *testing.T) {
	isolateGit(t)
	top := realTempDir(t)
	writeTree(t, top, map[string]string{
		"package.json":  "{}",
		"tsconfig.json": "{}",
		"src/café.ts":   "",
		"src/plain.ts":  "",
	})
	git(t, top, "init", "-q")
	t.Chdir(top)
	sel, msg, err := selectUnits(top, top, []string{"tsconfig.json"})
	if err != nil || msg != "" {
		t.Fatalf("%v %q", err, msg)
	}
	if want := map[string][]string{top: {"src/café.ts", "src/plain.ts"}}; !maps.EqualFunc(sel, want, slices.Equal) {
		t.Fatalf("got %v want %v; a file git quotes in ls-files output must still be selected, or a config change silently skips its tests and lint", sel, want)
	}
}
