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
	path := toolPath(t, "go")
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

func TestHookRemovedEventExitsTwoWithUsage(t *testing.T) {
	for _, event := range []string{"pre-tool-use", "post-tool-use", "subagent-stop"} {
		code, stdout, stderr := assure("hook", event)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: assure hook session-start|stop|protocol") {
			t.Errorf("hook %s: exit %d stdout %q stderr %q; a removed event is a usage error, never a silent allow", event, code, stdout, stderr)
		}
	}
}

func TestCheckRoleFlagExitsTwo(t *testing.T) {
	newRepo(t, levelB)
	for _, args := range [][]string{{"check", "--role", "verifier"}, {"check", "--fast", "--role", "verifier"}} {
		if code, _, stderr := assure(args...); code != 2 {
			t.Fatalf("%v: exit %d %q; the verifier role check is removed, so its flag is a usage error", args, code, stderr)
		}
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
	if code != 0 {
		t.Fatalf("got %d\n%s\n%s", code, stdout, stderr)
	}
	var ran []string
	for _, line := range strings.Split(stdout, "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 && (f[0] == "pass" || f[0] == "fail" || f[0] == "advisory-fail") {
			ran = append(ran, f[1]+" "+f[2])
		}
	}
	if len(ran) != 1 || ran[0] != "VER-TESTS-PASS go" {
		t.Fatalf("fast check decided %q, want only VER-TESTS-PASS for go: the Stop hook judges behaviour evidence, not code shape\n%s", ran, stdout)
	}
}

func TestCheckOutsideGitFailsClosed(t *testing.T) {
	newRepo(t, levelB)
	code, stdout, _ := assure("check", "--fast")
	if code != 1 || !strings.Contains(stdout, "fail\tchanged-files") {
		t.Fatalf("got %d %q", code, stdout)
	}
}
