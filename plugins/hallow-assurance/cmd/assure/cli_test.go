package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var adapterBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "assure-bin")
	if err != nil {
		panic(err)
	}
	build := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(dir, "assure-adapter-go"), "../../adapters/go/assure-adapter-go")
	if out, err := build.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("building assure-adapter-go: %v\n%s", err, out))
	}
	adapterBin = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type repo struct {
	t    *testing.T
	root string
}

func newRepo(t *testing.T, manifest string) repo {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := repo{t, root}
	if manifest != "" {
		r.write("assurance.yaml", manifest)
	}
	t.Chdir(root)
	t.Setenv("PATH", adapterBin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return r
}

func (r repo) write(rel, content string) {
	r.t.Helper()
	p := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func assure(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

const levelB = "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents:\n  - {path: 'low/**', level: C}\n  - {path: 'tools/**', level: D}\n"

func TestContextWithFreshCacheStartsNoAdapter(t *testing.T) {
	r := newRepo(t, levelB)
	bin := t.TempDir()
	marker := filepath.Join(r.root, "runs")
	script := "#!/bin/sh\necho run >> '" + marker + "'\nexec '" + filepath.Join(adapterBin, "assure-adapter-go") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	for range 3 {
		assure("context")
	}
	data, _ := os.ReadFile(marker)
	if n := strings.Count(string(data), "run"); n != 1 {
		t.Fatalf("adapter started %d times across three context calls; want 1", n)
	}
}

func TestContextScenarios(t *testing.T) {
	newRepo(t, "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents: []\n")
	code, out, stderr := assure("context")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !regexpLine(out, "VER-MUTATION-CHANGED", "required") {
		t.Errorf("VER-MUTATION-CHANGED not required at B:\n%s", out)
	}
	if regexpLine(out, "VER-COVERAGE-RESOLUTION", "required") {
		t.Errorf("VER-COVERAGE-RESOLUTION listed as required at B, where the catalog makes it advisory")
	}
	if strings.Contains(out, "FM-") {
		t.Errorf("formal objectives listed without a declaring component")
	}
	if strings.Contains(out, "VER-TRACE-REQ") || strings.Contains(out, "VER-DST-") {
		t.Errorf("context lists objectives no adapter can decide:\n%s", out)
	}
	if !strings.Contains(out, "assure reference go") {
		t.Errorf("Go adapter installed but context does not point at assure reference go:\n%s", out)
	}
	if _, again, _ := assure("context"); again != out {
		t.Error("context output differs between runs")
	}
}

func regexpLine(out, id, status string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == id && f[1] == status {
			return true
		}
	}
	return false
}

func TestContextFormalObjectivesUnderDeclaringComponent(t *testing.T) {
	newRepo(t, "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: C\ncomponents:\n  - path: 'ledger/**'\n    level: A\n    formal: {model: formal/L, challenge: formal/L/C.lean, link: drt}\n")
	_, out, _ := assure("context")
	i, j := strings.Index(out, "Objectives for ledger/**"), strings.Index(out, "FM-COMPLETE")
	if i < 0 || j < i {
		t.Fatalf("FM-COMPLETE not under the formal component:\n%s", out)
	}
}

func TestContextSurvivesMissingAdapter(t *testing.T) {
	newRepo(t, levelB)
	t.Setenv("PATH", t.TempDir())
	code, out, stderr := assure("context")
	if code != 0 || out == "" || !strings.Contains(stderr, "assure-adapter-go") {
		t.Fatalf("exit %d, stdout empty=%v, stderr %q", code, out == "", stderr)
	}
}

func TestContextFailsOnInvalidManifest(t *testing.T) {
	newRepo(t, "version: 0\ncatalog: v0\ndefault_level: B\ncomponents: []\n")
	code, out, stderr := assure("context")
	if code != 1 || out != "" || !strings.Contains(stderr, "languages") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, stderr)
	}
}

func TestClassifyScenarios(t *testing.T) {
	r := newRepo(t, "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents: []\n")
	r.write("internal/chat/stream_test.go", "package chat\n")
	code, out, stderr := assure("classify", "internal/chat/stream_test.go", "README.md")
	want := "internal/chat/stream_test.go\tB\tgo\ttest\nREADME.md\tB\t-\tunclassified\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, stderr)
	}
}

func TestClassifyWithoutManifest(t *testing.T) {
	newRepo(t, "")
	code, out, stderr := assure("classify", "a.go")
	if code != 1 || out != "" || !strings.Contains(stderr, "no assurance.yaml") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, stderr)
	}
}

func TestClassifyAdapterDisagreement(t *testing.T) {
	newRepo(t, "version: 0\ncatalog: v0\nlanguages: [aa, bb]\ndefault_level: B\ncomponents: []\n")
	bin := t.TempDir()
	for lang, role := range map[string]string{"aa": "config", "bb": "source"} {
		script := fmt.Sprintf("#!/bin/sh\nprintf '{\"protocol\":0,\"files\":[{\"path\":\"x.json\",\"language\":\"%s\",\"role\":\"%s\"}]}'\n", lang, role)
		if err := os.WriteFile(filepath.Join(bin, "assure-adapter-"+lang), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	code, out, stderr := assure("classify", "x.json")
	if code != 1 || out != "" || !strings.Contains(stderr, "assure-adapter-aa") || !strings.Contains(stderr, "assure-adapter-bb") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, stderr)
	}
}
