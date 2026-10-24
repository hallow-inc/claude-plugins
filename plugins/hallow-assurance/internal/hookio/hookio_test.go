package hookio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
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
	build := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(dir, "assure-adapter-go"), "../../adapters/go/assure-adapter-go")
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

const manifest = "version: 0\ncatalog: v0\nlanguages: [go]\ndefault_level: B\ncomponents:\n  - {path: 'tools/**', level: D}\n"

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
	for _, e := range []string{"session-start", "stop"} {
		if name == e || strings.HasPrefix(name, e+"-") {
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
			t.Errorf("%s: no event for this payload", name)
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

func TestWorktreeCwdManifestGoverns(t *testing.T) {
	outer, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setPath(t)
	t.Setenv("CLAUDE_PROJECT_DIR", outer)
	wt := filepath.Join(outer, "wt")
	writeFile(t, wt, "assurance.yaml", manifest)
	if code, out, _ := hook("session-start", payload(t, "session-start-startup", outer, nil)); code != 0 || out != nil {
		t.Fatalf("session root has no manifest, yet SessionStart there produced %d %v", code, out)
	}
	code, out, _ := hook("session-start", payload(t, "session-start-startup", wt, nil))
	ctx, _ := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if code != 0 || !strings.Contains(ctx, "Default level: B") {
		t.Fatalf("cwd inside a worktree with a manifest: got %d %v; the worktree's manifest must govern even though CLAUDE_PROJECT_DIR is the session root", code, out)
	}
}

func TestNotAdoptedIsSilent(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	setPath(t)
	for _, name := range []string{"session-start-startup", "stop-first"} {
		if code, out, _ := hook(eventFor(name), payload(t, name, root, nil)); code != 0 || out != nil {
			t.Errorf("%s: got %d %v", name, code, out)
		}
	}
}

func TestSessionStartInjectsContextAndWritesNoState(t *testing.T) {
	root := fixture(t)
	code, out, _ := hook("session-start", payload(t, "session-start-startup", root, nil))
	ctx, _ := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if code != 0 || !strings.Contains(ctx, "Default level: B") {
		t.Fatalf("got %d %v", code, out)
	}
	var written []string
	_ = filepath.WalkDir(filepath.Join(root, core.StateDir), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != filepath.Join(root, adapterproto.CacheFile) {
			written = append(written, p)
		}
		return nil
	})
	if len(written) > 0 {
		t.Fatalf("SessionStart wrote %v; apart from the adapter describe cache it has nothing to record, and session state outlives a branch switch", written)
	}
}

func TestContextFailureIsAWarning(t *testing.T) {
	root := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	code, out, _ := hook("session-start", payload(t, "session-start-startup", root, nil))
	ctx := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if code != 0 || !strings.Contains(ctx, "warning") || !strings.Contains(ctx, "assure-adapter-go") {
		t.Fatalf("missing adapter not reported as a warning: %d %q", code, ctx)
	}
}

func TestMalformedStopStdinBlocks(t *testing.T) {
	code, out, _ := hook("stop", []byte(`{"session_id":`))
	if reason, _ := out["reason"].(string); code != 2 || out["decision"] != "block" || !strings.Contains(reason, "failed closed") {
		t.Fatalf("got %d %v", code, out)
	}
	root := fixture(t)
	for _, bad := range []map[string]any{{"session_id": "a/b"}, {"session_id": ""}, {"cwd": "relative"}, {"hook_event_name": "SessionStart"}} {
		in := payload(t, "stop-first", root, func(m map[string]any) { maps.Copy(m, bad) })
		if code, out, _ := hook("stop", in); code != 2 || out["decision"] != "block" || !strings.Contains(out["reason"].(string), "failed closed") {
			t.Errorf("stop with %v: got %d %v; a session id that could escape the state directory must block as malformed input, before any state is read or written", bad, code, out)
		}
	}
}

func TestMalformedSessionStartStdinIsContext(t *testing.T) {
	code, out, _ := hook("session-start", []byte(`{"session_id":`))
	ctx, _ := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if code != 0 || !strings.Contains(ctx, "failed closed") {
		t.Fatalf("got %d %v; a SessionStart failure is reported as context, never a block", code, out)
	}
}

func TestRemovedEventsAreNotEvents(t *testing.T) {
	for _, e := range []string{"pre-tool-use", "post-tool-use", "subagent-stop"} {
		if IsEvent(e) {
			t.Errorf("%s is still a hook event; a v0.1 plugin calling it must get usage and exit 2", e)
		}
	}
	for _, e := range []string{"session-start", "stop"} {
		if !IsEvent(e) {
			t.Errorf("%s is not a hook event", e)
		}
	}
}

func TestPanicFailsClosed(t *testing.T) {
	orig := handler
	t.Cleanup(func() { handler = orig })
	handler = func(string, io.Reader) response { panic("boom") }
	code, out, _ := hook("stop", nil)
	if code != 2 || out["decision"] != "block" || !strings.Contains(out["reason"].(string), "boom") {
		t.Fatalf("stop panic: %d %v", code, out)
	}
	code, out, _ = hook("session-start", nil)
	if ctx, _ := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string); code != 0 || !strings.Contains(ctx, "boom") {
		t.Fatalf("session-start panic: %d %v", code, out)
	}
}

func TestWrongEventInputBlocksStop(t *testing.T) {
	root := fixture(t)
	if code, out, _ := hook("stop", payload(t, "session-start-startup", root, nil)); code != 2 || out["decision"] != "block" {
		t.Fatalf("session-start payload accepted as stop: %d %v", code, out)
	}
}

func TestBrokenManifestAtStopAllowsWithMessage(t *testing.T) {
	root := fixture(t)
	writeFile(t, root, "assurance.yaml", manifest+"authority: ci\n")
	_, loadErr := core.LoadManifest(filepath.Join(root, "assurance.yaml"))
	if loadErr == nil {
		t.Fatal("fixture manifest loads; the case needs one that does not")
	}
	code, out, _ := hook("stop", payload(t, "stop-first", root, nil))
	msg, _ := out["systemMessage"].(string)
	if code != 0 || out["decision"] != nil || !strings.Contains(msg, "authority") {
		t.Fatalf("got %d %v; the retry cap's state lives under the manifest root, so a block here would repeat with no cap: allow, and name the manifest error", code, out)
	}
}
