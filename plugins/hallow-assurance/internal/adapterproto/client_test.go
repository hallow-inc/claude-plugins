package adapterproto

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const goodDescribe = `{"protocol":1,"languages":["go"],"claims":["**/*.go"],"patterns":{"test":["**/*_test.go"]},"objectives":{}}`

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
		"non-zero exit":     {"echo adapter-broke >&2; exit 1", "stderr: adapter-broke"},
		"invalid JSON":      {"echo '{not json'", "invalid JSON"},
		"two documents":     {"echo '" + goodDescribe + goodDescribe + "'", "trailing data"},
		"schema violation":  {`echo '{"protocol": 1}'`, "violates the protocol"},
		"wrong language":    {`echo '` + strings.Replace(goodDescribe, `["go"]`, `["rust"]`, 1) + `'`, `does not include "go"`},
		"foreign reference": {`echo '` + strings.Replace(goodDescribe, `"objectives":{}`, `"objectives":{},"reference":"typescript"`, 1) + `'`, "/reference"},
		"timeout":           {"exec sleep 10", "killed after timeout of 2s"},
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

func TestOutputPastTheCapIsOverflowNotTimeout(t *testing.T) {
	exe := fakeAdapter(t, "go", "exec cat /dev/zero")
	out, err := invokeCapped(exe, t.TempDir(), "assure-adapter-go", "describe", 30*time.Second, 1<<10)
	var ae *Error
	if !errors.As(err, &ae) || ae.Adapter != "assure-adapter-go" || ae.Sub != "describe" {
		t.Fatalf("want *Error naming assure-adapter-go describe, got %v", err)
	}
	if !strings.Contains(err.Error(), "response exceeds 1024 bytes") {
		t.Fatalf("error %q does not report overflow of the 1024-byte cap", err)
	}
	if out != nil {
		t.Fatalf("overflowed call returned %d bytes of output", len(out))
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
	if d.Claims[0] != "**/*.go" || d.Patterns["test"][0] != "**/*_test.go" || string(raw) != goodDescribe || d.Reference != "" {
		t.Fatalf("parsed %+v raw %s", d, raw)
	}
	withRef := strings.Replace(goodDescribe, `"objectives":{}`, `"objectives":{},"reference":"go"`, 1)
	exe = fakeAdapter(t, "go", "echo '"+withRef+"'")
	if d, _, err = RunDescribe(exe, t.TempDir(), "go"); err != nil || d.Reference != "go" {
		t.Fatalf("reference within languages: got %+v, %v", d, err)
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

const batchRecorder = `shift; echo $# >> "$PWD/.batches"; printf '{"protocol":0,"files":['; sep=''; for p in "$@"; do printf '%s{"path":"%s","language":"go","role":"source"}' "$sep" "$p"; sep=','; done; printf ']}'`

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func pathOfLen(i, n int) string {
	p := fmt.Sprintf("p%d/", i)
	return p + strings.Repeat("x", n-len(p))
}

func argBytes(paths []string) int {
	n := 0
	for _, p := range paths {
		n += len(p) + 1
	}
	return n
}

func classifyBatches(t fataler, dir string, paths []string) []int {
	t.Helper()
	files, err := Classify(dir, "go", paths)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	got := make([]string, len(files))
	for i, f := range files {
		got[i] = f.Path
	}
	if !slices.Equal(got, paths) {
		t.Fatalf("classified %d paths, want each of the %d inputs once and in order", len(got), len(paths))
	}
	data, err := os.ReadFile(filepath.Join(dir, ".batches"))
	if err != nil {
		t.Fatalf("adapter recorded no invocation: %v", err)
	}
	var sizes []int
	for f := range strings.FieldsSeq(string(data)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("batch record %q: %v", f, err)
		}
		sizes = append(sizes, n)
	}
	return sizes
}

func TestClassifyBatchesAreMaximalRunsWithinTheArgumentLimit(t *testing.T) {
	fakeAdapter(t, "go", batchRecorder)
	base := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(base, "case")
		if err != nil {
			t.Fatalf("%v", err)
		}
		lens := rapid.SliceOfN(rapid.OneOf(rapid.IntRange(8, 64), rapid.IntRange(maxArgBytes/4, maxArgBytes-1)), 1, 8).Draw(t, "lengths")
		paths := make([]string, len(lens))
		for i, n := range lens {
			paths[i] = pathOfLen(i, n)
		}
		rest := paths
		for _, n := range classifyBatches(t, dir, paths) {
			if n < 1 || n > len(rest) {
				t.Fatalf("batch of %d with %d paths left: every path must be classified exactly once", n, len(rest))
			}
			batch := rest[:n]
			rest = rest[n:]
			if n > 1 && argBytes(batch) > maxArgBytes {
				t.Fatalf("batch of %d paths takes %d argument bytes, over the %d limit: the adapter exec fails with E2BIG", n, argBytes(batch), maxArgBytes)
			}
			if len(rest) > 0 && argBytes(batch)+len(rest[0])+1 <= maxArgBytes {
				t.Fatalf("batch of %d paths stopped at %d bytes though the next path fit: each extra batch is another adapter process", n, argBytes(batch))
			}
		}
		if len(rest) > 0 {
			t.Fatalf("%d paths never reached the adapter", len(rest))
		}
	})
}

func TestClassifyBatchLimitIsInclusive(t *testing.T) {
	fakeAdapter(t, "go", batchRecorder)
	half := maxArgBytes/2 - 1
	for extra, want := range []int{1, 2} {
		paths := []string{pathOfLen(0, half), pathOfLen(1, half+extra)}
		if got := classifyBatches(t, t.TempDir(), paths); len(got) != want {
			t.Fatalf("paths taking %d argument bytes (limit %d) went in %d batches %v, want %d", argBytes(paths), maxArgBytes, len(got), got, want)
		}
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
