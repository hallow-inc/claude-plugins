package qualification

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	gated  bool
	binDir string
)

var caseTools = []string{"go", "gofmt", "git", "golangci-lint", "gremlins"}

func TestMain(m *testing.M) {
	if real := os.Getenv("QUAL_REAL_ADAPTER"); real != "" {
		os.Exit(wrapAdapter(real, os.Getenv("QUAL_EMPTY_OBJECTIVE"), os.Getenv("QUAL_EMPTY_LOG"), os.Args[1:]))
	}
	flag.Parse()
	gated = os.Getenv("ASSURE_QUALIFICATION") == "1" && !testing.Short()
	if !gated {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "assure-qual-bin")
	if err != nil {
		panic(err)
	}
	for name, pkg := range map[string]string{
		"assure":            "../cmd/assure",
		"assure-adapter-go": "../adapters/go/assure-adapter-go",
	} {
		build := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(dir, name), pkg)
		if out, err := build.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			panic(fmt.Sprintf("building %s: %v\n%s", name, err, out))
		}
	}
	binDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestGoCases(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("go", "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		if gated {
			t.Fatal("no qualification cases under go/")
		}
		t.Skip("no qualification cases under go/")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txtar")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			c, err := parseCase(name, data)
			if err != nil {
				t.Fatal(err)
			}
			if !gated {
				t.Skip("set ASSURE_QUALIFICATION=1 without -short to run assure evaluate")
			}
			runCase(t, c)
		})
	}
}

func runCase(t *testing.T, c qcase) {
	t.Helper()
	ms, log := evaluateCase(t, c, filepath.Join(binDir, "assure-adapter-go"), nil)
	for _, m := range ms {
		t.Error(m)
	}
	if len(ms) > 0 {
		t.Log(log)
	}
}

func evaluateCase(t *testing.T, c qcase, adapter string, env []string) ([]string, string) {
	t.Helper()
	root := realTempDir(t)
	base, err := buildRepo(t.Context(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	allowed := slices.DeleteFunc(slices.Clone(caseTools), func(n string) bool { return slices.Contains(c.omit, n) })
	tools, err := hostTools(allowed)
	if err != nil {
		t.Fatal(err)
	}
	tools["assure"] = filepath.Join(binDir, "assure")
	tools["assure-adapter-go"] = adapter
	path, err := toolDir(t.TempDir(), tools)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.CommandContext(t.Context(), tools["assure"], "evaluate", "--changed-from", base, "--report", report, "--date", "2026-01-01")
	cmd.Dir = root
	cmd.Env = append(append(os.Environ(), "PATH="+path, "RAPID_SEED=1"), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		t.Fatalf("assure evaluate: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("reading report: %v\n%s", err, out.String())
	}
	rep, err := parseReport(data)
	if err != nil {
		t.Fatal(err)
	}
	return match(c.expect, rep), fmt.Sprintf("assure evaluate output:\n%s\nreport:\n%s", out.String(), data)
}

func TestCaseFilesAreNotHostGoFiles(t *testing.T) {
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to classify with the built assure")
	}
	var paths []string
	err := filepath.WalkDir("go", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, filepath.ToSlash(filepath.Join("qualification", p)))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no files under go/")
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "assure"), append([]string{"classify"}, paths...)...)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("assure classify: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(paths) {
		t.Fatalf("classified %d of %d files:\n%s", len(lines), len(paths), out)
	}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) != 4 {
			t.Fatalf("unexpected classify line %q", l)
		}
		if f[2] == "go" || f[3] == "source" || f[3] == "test" {
			t.Errorf("%s is classified as language %s, role %s; seeded cases must stay invisible to the host gate", f[0], f[2], f[3])
		}
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
