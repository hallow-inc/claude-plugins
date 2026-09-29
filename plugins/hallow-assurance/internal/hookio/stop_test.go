package hookio

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func goFixture(t *testing.T) string {
	t.Helper()
	return goFixtureWith(t, nil)
}

func goFixtureWith(t *testing.T, before func(root string)) string {
	t.Helper()
	root := fixture(t)
	setPath(t, "go", "golangci-lint")
	writeFile(t, root, ".gitignore", ".assure/state/\n")
	writeFile(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	writeFile(t, root, "p/p.go", "package p\n\nfunc One() int { return 1 }\n")
	if before != nil {
		before(root)
	}
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "init")
	hook("session-start", payload(t, "session-start-startup", root, nil))
	return root
}

func stopWith(t *testing.T, root string, active bool) (int, map[string]any) {
	t.Helper()
	name := "stop-first"
	if active {
		name = "stop-after-block"
	}
	code, out, _ := hook("stop", payload(t, name, root, nil))
	return code, out
}

func TestStopBlocksOnFailingTest(t *testing.T) {
	root := goFixture(t)
	writeFile(t, root, "p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) { t.Fatal(\"one is not two\") }\n")
	code, out := stopWith(t, root, false)
	reason, _ := out["reason"].(string)
	if code != 2 || out["decision"] != "block" || !strings.Contains(reason, "TestOne") || !strings.Contains(reason, "attempt 1 of 3") {
		t.Fatalf("got %d %v", code, out)
	}
}

func TestStopAllowsCleanChange(t *testing.T) {
	root := goFixture(t)
	writeFile(t, root, "p/p.go", "package p\n\nfunc One() int { return 2 - 1 }\n")
	if code, out := stopWith(t, root, false); code != 0 || out != nil {
		t.Fatalf("got %d %v", code, out)
	}
}

func TestStopBlocksOnDriftAndMissingSnapshot(t *testing.T) {
	root := goFixture(t)
	writeFile(t, root, ".assure/waivers.yaml", "[] # edited through Bash\n")
	_, out := stopWith(t, root, false)
	if reason, _ := out["reason"].(string); !strings.Contains(reason, "changed .assure/waivers.yaml") {
		t.Fatalf("drift not reported: %v", out)
	}
	if err := os.Remove(filepath.Join(root, ".assure", "state", "snapshot-"+session+".json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".assure/waivers.yaml", "[]\n")
	_, out = stopWith(t, root, false)
	if reason, _ := out["reason"].(string); !strings.Contains(reason, "snapshot for this session is missing") {
		t.Fatalf("missing snapshot not reported: %v", out)
	}
}

func TestStopEscalatesAfterThreeBlocks(t *testing.T) {
	root := goFixture(t)
	writeFile(t, root, "p/p_test.go", "package p\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) { t.Fatal(\"no\") }\n")
	for i := range 3 {
		if code, out := stopWith(t, root, i > 0); code != 2 || out["decision"] != "block" {
			t.Fatalf("stop %d: got %d %v", i+1, code, out)
		}
	}
	code, out := stopWith(t, root, true)
	msg, _ := out["systemMessage"].(string)
	if code != 0 || out["decision"] != nil || !strings.Contains(msg, "still fail after 3 attempts") {
		t.Fatalf("stop 4: got %d %v", code, out)
	}
}

func FuzzPreToolUse(f *testing.F) {
	root := fixture(f)
	writeFile(f, root, "assurance.yaml", manifest+"  - '**'\n")
	for _, name := range []string{"pre-tool-use-edit-main", "pre-tool-use-write-main", "pre-tool-use-notebookedit-main", "pre-tool-use-edit-subagent"} {
		f.Add(payload(f, name, root, nil))
	}
	f.Add([]byte(`{"hook_event_name":"PreToolUse","cwd":"` + root + `","tool_name":"Write","tool_input":{"file_path":"x"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var out, errb bytes.Buffer
		Run("pre-tool-use", bytes.NewReader(data), &out, &errb)
		if out.Len() > 0 && strings.Contains(out.String(), `"deny"`) {
			return
		}
		var in input
		if json.Unmarshal(data, &in) != nil || in.HookEventName != "PreToolUse" {
			t.Fatalf("undecodable input allowed: %q -> %q", data, out.String())
		}
		if !guardedTools[in.ToolName] {
			return
		}
		p := in.ToolInput.FilePath
		if in.ToolName == "NotebookEdit" {
			p = in.ToolInput.NotebookPath
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(in.Cwd, p)
		}
		if _, ok, _ := adopted(filepath.Dir(p)); ok {
			t.Fatalf("edit inside the all-protected repo allowed: %q -> %q", data, out.String())
		}
	})
}

func TestStopIgnoresBaselinedFinding(t *testing.T) {
	root := goFixtureWith(t, func(root string) {
		writeFile(t, root, "p/old.go", "package p\n\nfunc old() {}\n")
		gitIn(t, root, "init", "-q")
		gitIn(t, root, "add", "-A")
		gitIn(t, root, "commit", "-qm", "old finding")
		m, err := app.ManifestFor(root)
		if err != nil {
			t.Fatal(err)
		}
		b, err := app.Baseline(m)
		if err != nil || !slices.ContainsFunc(b.Entries, func(e core.BaselineEntry) bool { return e.Path == "p/old.go" && e.Objective == "CODE-ZERO-WARNINGS" }) {
			t.Fatalf("fixture has no baselined finding in p/old.go: %+v %v", b.Entries, err)
		}
		writeFile(t, root, core.BaselineFile, string(b.Marshal()))
	})
	writeFile(t, root, "p/old.go", "package p\n\n// the finding moves down a line\nfunc old() {}\n")
	if code, out := stopWith(t, root, false); code != 0 || out != nil {
		t.Fatalf("baselined finding blocked stopping: %d %v", code, out)
	}
}

func TestStopIgnoresExpiredWaiver(t *testing.T) {
	root := goFixtureWith(t, func(root string) {
		writeFile(t, root, core.WaiversFile, "- {objective: VER-TESTS-PASS, scope: '**', rationale: expired long before this test ran, approver: owner, expires: 2000-01-01}\n")
	})
	writeFile(t, root, "p/p.go", "package p\n\nfunc One() int { return 2 - 1 }\n")
	if code, out := stopWith(t, root, false); code != 0 || out != nil {
		t.Fatalf("expired waiver blocked stopping: %d %v", code, out)
	}
}
