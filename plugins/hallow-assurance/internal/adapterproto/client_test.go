package adapterproto

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const goodDescribe = `{"protocol":0,"languages":["go"],"claims":["**/*.go"],"patterns":{"test":["**/*_test.go"]},"objectives":{}}`

func fakeAdapter(t *testing.T, lang, script string) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, Executable(lang))
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	return exe
}

func TestDescribeFailureModesNameTheAdapter(t *testing.T) {
	cases := map[string]struct{ script, want string }{
		"non-zero exit":      {"echo adapter-broke >&2; exit 1", "stderr: adapter-broke"},
		"invalid JSON":       {"echo '{not json'", "invalid JSON"},
		"two documents":      {"echo '" + goodDescribe + goodDescribe + "'", "trailing data"},
		"schema violation":   {`echo '{"protocol": 0}'`, "violates the protocol"},
		"wrong language":     {`echo '` + strings.Replace(goodDescribe, `["go"]`, `["rust"]`, 1) + `'`, `does not include "go"`},
		"timeout":            {"exec sleep 10", "killed after timeout of 2s"},
		"oversized response": {"head -c 17000000 /dev/zero", "response exceeds"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			exe := fakeAdapter(t, "go", c.script)
			_, _, err := RunDescribe(exe, t.TempDir(), "go")
			var ae *Error
			if !errors.As(err, &ae) || ae.Adapter != "assure-adapter-go" || ae.Sub != "describe" {
				t.Fatalf("want *Error naming assure-adapter-go describe, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestMissingAdapterIsNamed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Resolve("go")
	if err == nil || !strings.Contains(err.Error(), "assure-adapter-go") {
		t.Fatalf("got %v", err)
	}
}

func TestDescribeParsesValidResponse(t *testing.T) {
	exe := fakeAdapter(t, "go", "echo '"+goodDescribe+"'")
	d, raw, err := RunDescribe(exe, t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if d.Claims[0] != "**/*.go" || d.Patterns["test"][0] != "**/*_test.go" || string(raw) != goodDescribe {
		t.Fatalf("parsed %+v raw %s", d, raw)
	}
}

func TestClassifyRunsInManifestDirAndBatches(t *testing.T) {
	fakeAdapter(t, "go", `printf '{"protocol":0,"files":['; sep=''; shift; for p in "$@"; do printf '%s{"path":"%s","language":"go","role":"source"}' "$sep" "$p"; sep=','; done; printf ']}'; pwd > "$PWD/.cwd"`)
	dir := t.TempDir()
	var paths []string
	for i := range 3000 {
		paths = append(paths, strings.Repeat("d", 60)+"/f"+strings.Repeat("x", i%10)+".go")
	}
	files, err := Classify(dir, "go", paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(paths) {
		t.Fatalf("got %d files for %d paths", len(files), len(paths))
	}
	cwd, err := os.ReadFile(filepath.Join(dir, ".cwd"))
	if err != nil {
		t.Fatalf("adapter did not run in the manifest directory: %v", err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(cwd))); got != mustEval(t, dir) {
		t.Fatalf("adapter cwd %s, want %s", got, dir)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOnlyStartFailuresAreUnstartable(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := Resolve("go"); !errors.Is(err, core.ErrAdapterUnstartable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not executable on PATH", func(t *testing.T) {
		exe := fakeAdapter(t, "go", "exit 0")
		if err := os.Chmod(exe, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve("go"); !errors.Is(err, core.ErrAdapterUnstartable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not executable at run", func(t *testing.T) {
		exe := fakeAdapter(t, "go", "exit 0")
		if err := os.Chmod(exe, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := RunDescribe(exe, t.TempDir(), "go"); !errors.Is(err, core.ErrAdapterUnstartable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("exits 1", func(t *testing.T) {
		exe := fakeAdapter(t, "go", "exit 1")
		_, _, err := RunDescribe(exe, t.TempDir(), "go")
		if err == nil || errors.Is(err, core.ErrAdapterUnstartable) {
			t.Fatalf("an adapter that started and failed: %v", err)
		}
	})
}
