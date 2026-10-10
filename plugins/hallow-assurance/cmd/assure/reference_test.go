package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func TestReferenceCommandScenarios(t *testing.T) {
	r := newRepo(t, levelB)
	real := filepath.Join(adapterBin, "assure-adapter-go")
	want, err := exec.CommandContext(context.Background(), real, "reference").Output()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(r.root, "calls")
	script := "#!/bin/sh\necho \"$1\" >> '" + marker + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")

	for range 2 {
		code, out, stderr := assure("reference", "go")
		if code != 0 || out != string(want) {
			t.Fatalf("exit %d, stdout equal to adapter output: %v, stderr %q", code, out == string(want), stderr)
		}
	}
	data, _ := os.ReadFile(marker)
	if calls := strings.Fields(string(data)); !slices.Equal(calls, []string{"describe", "reference", "reference"}) {
		t.Errorf("adapter calls %v; the second run must reuse the cached describe", calls)
	}

	code, out, stderr := assure("reference", "cobol")
	if code != 1 || out != "" || !slices.Contains(words(stderr), "go") {
		t.Errorf("unknown reference: exit %d, stdout %q, stderr %q; want exit 1 listing go", code, out, stderr)
	}
	for _, args := range [][]string{{"reference"}, {"reference", "go", "go"}} {
		code, out, stderr := assure(args...)
		if code != 2 || out != "" || !strings.Contains(stderr, "usage") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want exit 2 with usage", args, code, out, stderr)
		}
	}
}

func fakeReferenceAdapter(t *testing.T, bin, lang, ref, referenceScript string) {
	t.Helper()
	field := ""
	if ref != "" {
		field = `,"reference":"` + ref + `"`
	}
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":1,"languages":["` + lang + `"],"claims":["**/*.` + lang + `"],"patterns":{},"objectives":{}` + field + `}' ;;
reference) ` + referenceScript + ` ;;
*) echo "assure-adapter-` + lang + `: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceAdapterFailuresNameTheAdapter(t *testing.T) {
	newRepo(t, "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n")
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	cases := []struct {
		script, stdout string
		ok             bool
	}{
		{script: `printf '# XX\nbody'`, stdout: "# XX\nbody", ok: true},
		{script: `head -c 262144 /dev/zero | tr '\0' a`, stdout: strings.Repeat("a", 256<<10), ok: true},
		{script: `head -c 307200 /dev/zero | tr '\0' a`},
		{script: `head -c 262145 /dev/zero | tr '\0' a`},
		{script: `echo '# partial'; exit 3`},
		{script: `printf ' \n\t\n'`},
		{script: `:`},
	}
	for _, c := range cases {
		fakeReferenceAdapter(t, bin, "xx", "xx", c.script)
		code, out, stderr := assure("reference", "xx")
		if c.ok {
			if code != 0 || out != c.stdout {
				t.Errorf("%s: exit %d, stdout %d bytes (want %d), stderr %q", c.script, code, len(out), len(c.stdout), stderr)
			}
			continue
		}
		if code != 1 || out != "" || !strings.Contains(stderr, "assure-adapter-xx") {
			t.Errorf("%s: exit %d, stdout %d bytes, stderr %q; want exit 1 naming assure-adapter-xx", c.script, code, len(out), stderr)
		}
	}
}

func TestContextListsEveryReferenceDeterministically(t *testing.T) {
	newRepo(t, "version: 0\ncatalog: v0\nlanguages: [xx, yy, zz, ww]\ndefault_level: B\ncomponents: []\n")
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	for _, l := range []string{"xx", "yy", "zz"} {
		fakeReferenceAdapter(t, bin, l, l, "echo '# "+l+"'")
	}
	fakeReferenceAdapter(t, bin, "ww", "", "echo '# ww'")
	code, first, stderr := assure("context")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, l := range []string{"xx", "yy", "zz"} {
		if !strings.Contains(first, "assure reference "+l) {
			t.Errorf("context does not list assure reference %s:\n%s", l, first)
		}
	}
	if strings.Contains(first, "assure reference ww") {
		t.Error("context lists a reference for an adapter whose describe declares none")
	}
	for range 8 {
		if _, again, _ := assure("context"); again != first {
			t.Fatalf("context output differs between runs:\n%s\n---\n%s", first, again)
		}
	}
	_, _, stderr = assure("reference", "cobol")
	if got := words(stderr); !slices.Contains(got, "xx") || !slices.Contains(got, "yy") || !slices.Contains(got, "zz") || slices.Contains(got, "ww") {
		t.Errorf("unknown reference must list exactly the available references xx, yy, zz; stderr %q", stderr)
	}
}
