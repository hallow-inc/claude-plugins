//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLintWaitsForAnotherGolangciRun(t *testing.T) {
	needGolangci(t)
	dir := committedModule(t, map[string]string{"x/a.go": "package x\n"})
	writeFiles(t, dir, map[string]string{"x/a.go": "package x\n\nfunc A() int { return 1 }\n"})
	// golangci-lint gives up on a held lock after about 5 s (measured with 2.13.2).
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f, err := os.Create(filepath.Join(tmp, "golangci-lint.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	released := time.AfterFunc(7*time.Second, func() { _ = f.Close() })
	defer released.Stop()
	for _, r := range lintSARIF(t, dir, "CODE-ZERO-WARNINGS") {
		if strings.Contains(r.Message, "parallel golangci-lint is running") {
			t.Fatalf("a concurrent golangci-lint (another pack, an editor) turned into a finding: %s", r.Message)
		}
	}
}
