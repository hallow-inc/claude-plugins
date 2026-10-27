package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func junitOf(t interface{ Fatalf(string, ...any) }, events []event) ev.JUnit {
	data, err := xml.Marshal(toJUnit(events))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	return j
}

func TestConversionPreservesFailures(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var events []event
		want := 0
		for p := range rapid.IntRange(1, 3).Draw(t, "npkgs") {
			pkg := fmt.Sprintf("example.com/m/p%d", p)
			anyFailed := false
			for i := range rapid.IntRange(0, 3).Draw(t, "ntests") {
				name := fmt.Sprintf("Test%d", i)
				if rapid.Bool().Draw(t, "subtest") {
					name += "/case"
				}
				events = append(events, event{Action: "run", Package: pkg, Test: name},
					event{Action: "output", Package: pkg, Test: name, Output: "=== RUN " + name + "\n"})
				switch rapid.SampledFrom([]string{"pass", "fail", "skip", "none"}).Draw(t, "final") {
				case "fail":
					anyFailed = true
					want++
					events = append(events, event{Action: "fail", Package: pkg, Test: name})
				case "pass":
					events = append(events, event{Action: "pass", Package: pkg, Test: name})
				case "skip":
					events = append(events, event{Action: "skip", Package: pkg, Test: name})
				}
			}
			switch rapid.SampledFrom([]string{"pass", "fail", "build", "none"}).Draw(t, "pkgFinal") {
			case "fail":
				if !anyFailed {
					want++
				}
				events = append(events, event{Action: "fail", Package: pkg})
			case "build":
				ip := pkg + " [" + pkg + ".test]"
				events = append(events, event{Action: "build-output", ImportPath: ip, Output: "x.go:1:1: bad\n"},
					event{Action: "build-fail", ImportPath: ip},
					event{Action: "fail", Package: pkg, FailedBuild: ip})
				if !anyFailed {
					want++
				}
			case "pass":
				events = append(events, event{Action: "pass", Package: pkg})
			}
		}
		if rapid.Bool().Draw(t, "orphanBuildFail") {
			events = append(events, event{Action: "build-output", ImportPath: "./missing", Output: "no dir\n"},
				event{Action: "build-fail", ImportPath: "./missing"})
			want++
		}
		j := junitOf(t, events)
		if got := j.Failures + j.Errors; got != want {
			t.Fatalf("failures+errors = %d, want %d", got, want)
		}
	})
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "core.excludesFile=/dev/null"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func committedModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.26\n"})
	writeFiles(t, dir, files)
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

func selection(t *testing.T, dir string) map[string][]string {
	t.Helper()
	paths, err := adapterkit.ChangedPaths(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return selectPackages(dir, paths)
}

func TestChangedPackageSelection(t *testing.T) {
	base := map[string]string{
		"a/a.go": "package a\n", "b/b.go": "package b\n",
		"c/d/one.go": "package d\n", "c/d/two.go": "package d\n",
		"gone/only.go": "package gone\n", "README.md": "x\n",
	}
	t.Run("edited, untracked, and surviving-package delete", func(t *testing.T) {
		dir := committedModule(t, base)
		writeFiles(t, dir, map[string]string{"a/a.go": "package a\n\nvar X = 1\n", "b/new.go": "package b\n"})
		git(t, dir, "rm", "-q", "c/d/two.go", "gone/only.go")
		want := map[string][]string{".": {"./a", "./b", "./c/d"}}
		if got := selection(t, dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("go.mod selects the module", func(t *testing.T) {
		dir := committedModule(t, base)
		writeFiles(t, dir, map[string]string{"go.mod": "module example.com/m\n\ngo 1.26.0\n", "a/a.go": "package a\n\nvar Y = 2\n"})
		want := map[string][]string{".": {"./..."}}
		if got := selection(t, dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("non-Go change selects nothing", func(t *testing.T) {
		dir := committedModule(t, base)
		writeFiles(t, dir, map[string]string{"README.md": "y\n", "a/testdata/x.go": "package x\n"})
		if got := selection(t, dir); len(got) != 0 {
			t.Fatalf("got %v, want nothing", got)
		}
	})
}

func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestTrackedNonASCIIFileSelectsItsPackage(t *testing.T) {
	isolateGitConfig(t)
	for _, name := range []string{"cafe.go", "café.go"} {
		dir := committedModule(t, map[string]string{"p/" + name: "package p\n"})
		writeFiles(t, dir, map[string]string{"p/" + name: "package p\n\nvar X = 1\n"})
		want := map[string][]string{".": {"./p"}}
		if got := selection(t, dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("modified %s: got %v, want %v; git's default core.quotePath prints this name quoted in diff output, and a quoted name that is not decoded drops its package from selection, silently skipping its tests", name, got, want)
		}
	}
}

func TestUntrackedNonASCIIFileSelectsItsPackage(t *testing.T) {
	isolateGitConfig(t)
	for _, name := range []string{"cafe.go", "café.go"} {
		dir := committedModule(t, map[string]string{"README.md": "x\n"})
		writeFiles(t, dir, map[string]string{"p/" + name: "package p\n"})
		want := map[string][]string{".": {"./p"}}
		if got := selection(t, dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("untracked %s: got %v, want %v; git's default core.quotePath prints this name quoted in ls-files output, and a quoted name that is not decoded drops its package from selection, silently skipping its tests", name, got, want)
		}
	}
}

func runIn(t *testing.T, dir string, args ...string) (runResponse, string, int) {
	t.Helper()
	resp, _, stderr, code := runRawIn(t, dir, args...)
	return resp, stderr, code
}

func runRawIn(t *testing.T, dir string, args ...string) (runResponse, []byte, string, int) {
	t.Helper()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	if code != 0 {
		return runResponse{}, nil, errb.String(), code
	}
	if vs, err := schemas.Validate(schemas.AdapterRun, out.Bytes()); err != nil || len(vs) > 0 {
		t.Fatalf("run response invalid: %v %v\n%s", err, vs, out.Bytes())
	}
	var resp runResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Protocol != 2 {
		t.Errorf("run response protocol %d, want 2; run gained the optional untracked list, and an assure that predates it must refuse the response as a protocol mismatch\n%s", resp.Protocol, out.Bytes())
	}
	return resp, out.Bytes(), errb.String(), code
}

func runUntracked(t *testing.T, raw []byte) ([]string, bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	f, ok := fields["untracked"]
	if !ok {
		return nil, false
	}
	var names []string
	if err := json.Unmarshal(f, &names); err != nil {
		t.Fatalf("untracked %s is not a list of tool names: %v", f, err)
	}
	return names, true
}

const failingTest = "package a\n\nimport \"testing\"\n\nfunc TestNo(t *testing.T) { t.Fatal(\"no\") }\n"

func TestRunTestsFailingTestStillExitsZero(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"a/a_test.go": failingTest, "bad/bad.go": "package bad\n\nfunc F() int { return \"x\" }\n"})
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(resp.Evidence) != 1 || resp.Evidence[0].Type != "test.junit" || resp.ToolVersions["go"] == "" {
		t.Fatalf("response %+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	if j.Failures != 1 || j.Errors != 1 {
		t.Fatalf("want one failing test and one build error, got %+v", j)
	}
	i := slices.IndexFunc(j.Failing, func(f ev.Failure) bool { return f.Error })
	if i < 0 || !strings.Contains(j.Failing[i].Message+j.Failing[i].Text, "bad.go") {
		t.Fatalf("erroring case %+v; its text must carry the compiler's message, or the agent cannot see why the package failed", j.Failing)
	}
	if _, err := os.Stat(filepath.Join(out, "lint.sarif")); err == nil {
		t.Fatal("VER-TESTS-PASS wrote lint.sarif; no objective reads it any more")
	}
}

func TestRunNoChangedGoFilesWritesEmptyEvidence(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n", "README.md": "x\n"})
	writeFiles(t, dir, map[string]string{"README.md": "y\n"})
	out := filepath.Join(t.TempDir(), "ev")
	if _, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, _ := os.ReadFile(filepath.Join(out, "junit.xml"))
	if j, err := ev.ParseJUnit(data); err != nil || j.Cases != 0 {
		t.Fatalf("want empty JUnit, got %+v %v", j, err)
	}
}

func TestRunWithoutGoIsEnvironment(t *testing.T) {
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	narrowPath(t, []string{"git"}, nil)
	out := filepath.Join(t.TempDir(), "ev")
	resp, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s; a missing Go toolchain is the environment's fault, not an adapter crash", code, stderr)
	}
	environmentIn(t, resp, out, "go is not on PATH")
}

func TestRunUnknownObjective(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"run", "VER-NOT-AN-OBJECTIVE", "--changed-from", "HEAD", "--out", t.TempDir()}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "not implemented") {
		t.Fatalf("exit %d, stderr %q; an unimplemented objective must exit 2 naming it", code, errb.String())
	}
}

func TestRunTestsRunsInShortMode(t *testing.T) {
	const onlyShort = "package a\n\nimport \"testing\"\n\nfunc TestSlow(t *testing.T) {\n\tif !testing.Short() {\n\t\tt.Fatal(\"not short\")\n\t}\n}\n"
	dir := committedModule(t, map[string]string{"a/a.go": "package a\n"})
	writeFiles(t, dir, map[string]string{"a/a_test.go": onlyShort})
	out := filepath.Join(t.TempDir(), "ev")
	if _, stderr, code := runIn(t, dir, "run", "VER-TESTS-PASS", "--changed-from", "HEAD", "--out", out); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := ev.ParseJUnit(data)
	if err != nil {
		t.Fatal(err)
	}
	if j.Cases == 0 || j.Failures != 0 || j.Errors != 0 {
		t.Fatalf("want TestSlow run and passing under -short, got %+v", j)
	}
}

func TestCapTextBoundsEvidenceTextAndNeverEmpty(t *testing.T) {
	check := func(t interface{ Fatalf(string, ...any) }, in string) {
		got := capText([]byte(in))
		trimmed := strings.TrimSpace(in)

		if got == "" {
			t.Fatalf("capText(%q) is empty; evidence messages must never be blank", in)
		}
		if len(got) > maxOutput {
			t.Fatalf("len(capText) = %d > maxOutput %d; stderr would bloat evidence", len(got), maxOutput)
		}
		if trimmed == "" {
			if got != "no output" {
				t.Fatalf("capText(%q) = %q, want \"no output\"", in, got)
			}
			return
		}
		if !utf8.ValidString(got) {
			t.Fatalf("capText result (%d bytes, ends %q) is not valid UTF-8; it lands in junit.xml and error strings, where a split rune corrupts the evidence", len(got), got[max(0, len(got)-4):])
		}
		if !strings.HasPrefix(trimmed, got) {
			t.Fatalf("capText result is not a prefix of the trimmed input")
		}
		if len(trimmed) <= maxOutput {
			if len(got) != len(trimmed) {
				t.Fatalf("len(capText) = %d, want %d: text dropped below the bound", len(got), len(trimmed))
			}
		} else if len(got) < maxOutput-(utf8.UTFMax-1) {
			t.Fatalf("len(capText) = %d < %d: text dropped beyond backing up to a rune boundary", len(got), maxOutput-(utf8.UTFMax-1))
		}
	}

	check(t, "a"+strings.Repeat("é", maxOutput/2))

	rapid.Check(t, func(t *rapid.T) {
		pad := rapid.SampledFrom([]string{"", " ", "\n", "\t \r\n"})
		var body string
		switch rapid.IntRange(0, 2).Draw(t, "shape") {
		case 0:
			body = rapid.SampledFrom([]string{"", " ", "\n\t "}).Draw(t, "blank")
		case 1:
			body = rapid.String().Draw(t, "short")
		case 2:
			chunk := rapid.StringN(1, 8, -1).Draw(t, "chunk")
			extra := rapid.IntRange(0, 64).Draw(t, "extra")
			body = strings.Repeat(chunk, (maxOutput+extra)/len(chunk)+1)
		}
		check(t, pad.Draw(t, "lead")+body+pad.Draw(t, "trail"))
	})
}
