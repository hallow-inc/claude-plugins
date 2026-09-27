package hookio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var adapterBin, origPath string

func TestMain(m *testing.M) {
	if dir := os.Getenv("ASSURE_HOOKIO_ADAPTER_BIN"); dir != "" {
		adapterBin, origPath = dir, os.Getenv("ASSURE_HOOKIO_ORIG_PATH")
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "assure-hookio-bin")
	if err != nil {
		panic(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "assure-adapter-go"), "../../adapters/go")
	if out, err := build.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("building assure-adapter-go: %v\n%s", err, out))
	}
	adapterBin, origPath = dir, os.Getenv("PATH")
	_ = os.Setenv("ASSURE_HOOKIO_ADAPTER_BIN", dir)
	_ = os.Setenv("ASSURE_HOOKIO_ORIG_PATH", origPath)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const manifest = "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents:\n  - {path: 'tools/**', level: D}\nprotected: ['.github/workflows/**']\n"

const session = "00000000-0000-4000-8000-000000000001"

func setPath(t testing.TB, tools ...string) {
	t.Helper()
	dirs := []string{adapterBin}
	t.Setenv("PATH", origPath)
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH", tool)
		}
		dirs = append(dirs, filepath.Dir(p))
	}
	t.Setenv("PATH", strings.Join(append(dirs, "/usr/bin", "/bin"), string(os.PathListSeparator)))
}

func fixture(t testing.TB) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "assurance.yaml", manifest)
	writeFile(t, root, ".assure/waivers.yaml", "[]\n")
	setPath(t)
	return root
}

func writeFile(t testing.TB, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func payload(t testing.TB, name, root string, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "hooks", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("/work/repo"), []byte(root))
	if edit == nil {
		return data
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, _ := json.Marshal(m)
	return out
}

func toolFile(path string) func(map[string]any) {
	return func(m map[string]any) {
		ti := m["tool_input"].(map[string]any)
		if _, ok := ti["notebook_path"]; ok {
			ti["notebook_path"] = path
		} else {
			ti["file_path"] = path
		}
	}
}

func hook(event string, in []byte) (int, map[string]any, string) {
	var out, errb bytes.Buffer
	code := Run(event, bytes.NewReader(in), &out, &errb)
	if out.Len() == 0 {
		return code, nil, errb.String()
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		return code, map[string]any{"unparsable": out.String()}, errb.String()
	}
	return code, m, errb.String()
}

func eventFor(name string) string {
	for _, e := range []string{"session-start", "pre-tool-use", "stop"} {
		if strings.HasPrefix(name, e+"-") {
			return e
		}
	}
	return ""
}

func TestRecordedPayloadsDecode(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "hooks", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recorded payloads: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		event := eventFor(name)
		if event == "" {
			if !strings.HasPrefix(name, "subagent-stop") {
				t.Errorf("%s: no event for this payload", name)
			}
			continue
		}
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			code, out, _ := hook(event, payload(t, name, root, nil))
			if code != 0 && code != 2 {
				t.Fatalf("exit %d", code)
			}
			if s := fmt.Sprint(out); strings.Contains(s, "failed closed") || strings.Contains(s, "unparsable") {
				t.Fatalf("payload did not decode: %v", out)
			}
		})
	}
}

func decision(out map[string]any) (string, string) {
	hso, _ := out["hookSpecificOutput"].(map[string]any)
	d, _ := hso["permissionDecision"].(string)
	r, _ := hso["permissionDecisionReason"].(string)
	return d, r
}

func TestPreToolUseGolden(t *testing.T) {
	cases := []struct {
		name, payload, path, agent, reasonHas string
		deny                                  bool
	}{
		{"protected waiver", "pre-tool-use-edit-main", ".assure/waivers.yaml", "", "invariant 6", true},
		{"verifier edits source", "pre-tool-use-edit-subagent", "x.go", core.Verifier, "may not edit source", true},
		{"local settings", "pre-tool-use-write-main", ".claude/settings.local.json", "", "invariant 6", true},
		{"project settings via notebook tool", "pre-tool-use-notebookedit-main", ".claude/settings.json", "", "invariant 6", true},
		{"allowed edit", "pre-tool-use-edit-main", "notes.txt", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := fixture(t)
			in := payload(t, c.payload, root, func(m map[string]any) {
				toolFile(filepath.Join(root, c.path))(m)
				if c.agent != "" {
					m["agent_type"] = c.agent
				}
			})
			code, out, stderr := hook("pre-tool-use", in)
			if !c.deny {
				if code != 0 || out != nil {
					t.Fatalf("want silent allow, got %d %v", code, out)
				}
				return
			}
			d, reason := decision(out)
			hso, _ := out["hookSpecificOutput"].(map[string]any)
			if code != 2 || d != "deny" || hso["hookEventName"] != "PreToolUse" || !strings.Contains(reason, c.reasonHas) || !strings.Contains(stderr, c.reasonHas) {
				t.Fatalf("got %d %v stderr=%q", code, out, stderr)
			}
		})
	}
}

func TestWorktreeManifestGovernsTheEdit(t *testing.T) {
	outer, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setPath(t)
	wt := filepath.Join(outer, "wt")
	writeFile(t, wt, "assurance.yaml", manifest)
	in := payload(t, "pre-tool-use-edit-main", outer, toolFile(filepath.Join(wt, ".assure", "waivers.yaml")))
	if code, out, _ := hook("pre-tool-use", in); code != 2 {
		t.Fatalf("worktree waiver edit allowed: %v", out)
	}
}

func TestNotAdoptedIsSilent(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	setPath(t)
	for _, name := range []string{"pre-tool-use-edit-main", "session-start-startup", "stop-first"} {
		if code, out, _ := hook(eventFor(name), payload(t, name, root, nil)); code != 0 || out != nil {
			t.Errorf("%s: got %d %v", name, code, out)
		}
	}
}

func TestSessionStartInjectsContextAndSnapshots(t *testing.T) {
	root := fixture(t)
	code, out, _ := hook("session-start", payload(t, "session-start-startup", root, nil))
	ctx, _ := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if code != 0 || !strings.Contains(ctx, "Default level: B") {
		t.Fatalf("got %d %v", code, out)
	}
	if _, err := os.Stat(core.SnapshotPath(root, session)); err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
}

func TestContextFailureStillSnapshots(t *testing.T) {
	root := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	_, out, _ := hook("session-start", payload(t, "session-start-startup", root, nil))
	ctx := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if !strings.Contains(ctx, "warning") || !strings.Contains(ctx, "assure-adapter-go") {
		t.Fatalf("missing adapter not reported: %q", ctx)
	}
	if _, err := os.Stat(core.SnapshotPath(root, session)); err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
}

func TestCompactDoesNotReplaceTheSnapshot(t *testing.T) {
	root := fixture(t)
	hook("session-start", payload(t, "session-start-startup", root, nil))
	writeFile(t, root, ".assure/waivers.yaml", "[] # edited through Bash\n")
	hook("session-start", payload(t, "session-start-startup", root, func(m map[string]any) { m["source"] = "compact" }))
	m, err := core.LoadManifest(filepath.Join(root, "assurance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if d := drift(m, session); len(d) != 1 || !strings.Contains(d[0], ".assure/waivers.yaml") {
		t.Fatalf("drift after compact = %v", d)
	}
}

func TestMalformedStdinDenies(t *testing.T) {
	code, out, _ := hook("pre-tool-use", []byte(`{"tool_name":`))
	if d, reason := decision(out); code != 2 || d != "deny" || !strings.Contains(reason, "failed closed") {
		t.Fatalf("got %d %v", code, out)
	}
}

func TestPanicFailsClosed(t *testing.T) {
	orig := handler
	t.Cleanup(func() { handler = orig })
	handler = func(string, io.Reader) response { panic("boom") }
	if code, out, _ := hook("pre-tool-use", nil); code != 2 {
		t.Fatalf("pre-tool-use panic: %d %v", code, out)
	}
	code, out, _ := hook("stop", nil)
	if code != 2 || out["decision"] != "block" || !strings.Contains(out["reason"].(string), "boom") {
		t.Fatalf("stop panic: %d %v", code, out)
	}
}

func TestWrongEventInputDenies(t *testing.T) {
	root := fixture(t)
	if code, _, _ := hook("pre-tool-use", payload(t, "stop-first", root, nil)); code != 2 {
		t.Fatal("stop payload accepted as pre-tool-use")
	}
}
