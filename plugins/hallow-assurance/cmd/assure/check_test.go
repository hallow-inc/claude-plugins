package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func (r repo) git(args ...string) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = r.root
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func toolPath(t *testing.T, tools ...string) string {
	t.Helper()
	dirs := []string{adapterBin}
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH", tool)
		}
		dirs = append(dirs, filepath.Dir(p))
	}
	return strings.Join(append(dirs, "/usr/bin", "/bin"), string(os.PathListSeparator))
}

func goRepo(t *testing.T) repo {
	t.Helper()
	path := toolPath(t, "go", "golangci-lint")
	r := newRepo(t, levelB)
	t.Setenv("PATH", path)
	r.write(".gitignore", ".assure/state/\n")
	r.write("go.mod", "module example.com/m\n\ngo 1.26\n")
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return a + b }\n")
	r.write("p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n")
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	return r
}

func TestCheckWithoutFastExitsTwo(t *testing.T) {
	newRepo(t, levelB)
	if code, _, stderr := assure("check"); code != 2 || !strings.Contains(stderr, "assure evaluate") {
		t.Fatalf("got %d %q", code, stderr)
	}
}

func TestCheckNothingChangedRunsNoAdapter(t *testing.T) {
	r := newRepo(t, levelB)
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "assure-adapter-go"), []byte("#!/bin/sh\necho ran >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+"/usr/bin:/bin")
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	code, stdout, stderr := assure("check", "--fast")
	if code != 0 || !strings.Contains(stdout, "no changed files") {
		t.Fatalf("got %d %q %q", code, stdout, stderr)
	}
}

func TestCheckUntrackedFailingTestBlocks(t *testing.T) {
	r := goRepo(t)
	r.write("p/q_test.go", "package p\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"broken\") }\n")
	code, stdout, stderr := assure("check", "--fast")
	if code != 1 || !strings.Contains(stdout, "fail\tVER-TESTS-PASS\tgo") || !strings.Contains(stdout, "TestBroken") {
		t.Fatalf("got %d\n%s\n%s", code, stdout, stderr)
	}
}

func TestCheckCleanChangePasses(t *testing.T) {
	r := goRepo(t)
	r.write("p/p.go", "package p\n\nfunc Add(a, b int) int { return b + a }\n")
	code, stdout, stderr := assure("check", "--fast")
	if code != 0 || !strings.Contains(stdout, "pass\tVER-TESTS-PASS\tgo") || !strings.Contains(stdout, "pass\tCODE-ZERO-WARNINGS\tgo") || strings.Contains(stdout, "CODE-COMPLEXITY") {
		t.Fatalf("got %d\n%s\n%s", code, stdout, stderr)
	}
}

func TestCheckOutsideGitFailsClosed(t *testing.T) {
	newRepo(t, levelB)
	code, stdout, _ := assure("check", "--fast")
	if code != 1 || !strings.Contains(stdout, "fail\tchanged-files") {
		t.Fatalf("got %d %q", code, stdout)
	}
}

func TestSnapshotThenDrift(t *testing.T) {
	r := newRepo(t, levelB)
	r.write(".assure/waivers.yaml", "[]\n")
	if code, _, stderr := assure("guard", "--snapshot", "--session", "s1"); code != 0 {
		t.Fatalf("snapshot: %d %s", code, stderr)
	}
	if code, stdout, _ := assure("guard", "--drift", "--session", "s1"); code != 0 || stdout != "" {
		t.Fatalf("clean drift: %d %q", code, stdout)
	}
	r.write("src/x.go", "package x\n")
	if code, _, _ := assure("guard", "--drift", "--session", "s1"); code != 0 {
		t.Fatal("unprotected edit reported as drift")
	}
	r.write(".assure/waivers.yaml", "[] # edited through Bash\n")
	code, stdout, _ := assure("guard", "--drift", "--session", "s1")
	if code != 1 || !strings.Contains(stdout, "changed .assure/waivers.yaml") {
		t.Fatalf("got %d %q", code, stdout)
	}
	if code, stdout, _ := assure("guard", "--drift", "--session", "s2"); code != 1 || !strings.Contains(stdout, "snapshot missing") {
		t.Fatalf("missing snapshot: %d %q", code, stdout)
	}
}

func TestSnapshotRejectsBadSessionAndWritesNothing(t *testing.T) {
	r := newRepo(t, levelB)
	for _, id := range []string{"../x", "a/b", ""} {
		if code, _, _ := assure("guard", "--snapshot", "--session", id); code != 2 {
			t.Errorf("session %q: exit %d, want 2", id, code)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, ".assure")); err == nil {
		t.Fatal("bad session wrote state")
	}
}
