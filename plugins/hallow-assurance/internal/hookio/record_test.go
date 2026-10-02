package hookio

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	root := fixture(t)
	writeFile(t, root, ".gitignore", ".assure/state/\n")
	writeFile(t, root, "notes.txt", "hey\n")
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "init")
	return root
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

type pendingEntry struct {
	Path string  `json:"path"`
	Pre  *string `json:"pre"`
}

func readPending(t *testing.T, root, toolUseID string) (pendingEntry, bool) {
	t.Helper()
	data, err := os.ReadFile(app.PendingPath(root, session, toolUseID))
	if os.IsNotExist(err) {
		return pendingEntry{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.Pending, data); err != nil || len(vs) > 0 {
		t.Fatalf("pending entry violates pending.schema.json: %v %v\n%s", err, vs, data)
	}
	var p pendingEntry
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p, true
}

func provenanceLines(t *testing.T, root string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, core.ProvenanceDir, session+".jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.Provenance, data); err != nil || len(vs) > 0 {
		t.Fatalf("provenance file violates provenance.schema.json: %v %v\n%s", err, vs, data)
	}
	var out []map[string]any
	for l := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func noPendingState(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, core.StateDir, "pending")); !os.IsNotExist(err) {
		t.Fatalf("pending state exists: %v", err)
	}
}

var pendingScenarios = []struct {
	name string
	run  func(t *testing.T, root string)
}{
	{"allowed edit pends the clean-filtered blob", func(t *testing.T, root string) {
		writeFile(t, root, ".gitattributes", "*.txt text\n")
		writeFile(t, root, "notes.txt", "hey\r\nthere\r\n")
		if code, out, _ := hook("pre-tool-use", payload(t, "pre-tool-use-edit-subagent-paired", root, nil)); code != 0 || out != nil {
			t.Fatalf("allowed edit: got %d %v", code, out)
		}
		p, ok := readPending(t, root, "toolu_fixture_sub_edit")
		want := gitOut(t, root, "hash-object", "--path", "notes.txt", "notes.txt")
		if !ok || p.Path != "notes.txt" || p.Pre == nil || *p.Pre != want {
			t.Fatalf("pending %+v %v, want path notes.txt and blob %s", p, ok, want)
		}
		if raw := gitOut(t, root, "hash-object", "--no-filters", "notes.txt"); raw == want {
			t.Fatal("fixture does not exercise clean filters: filtered and raw hashes agree")
		}
	}},
	{"write of a new file pends null", func(t *testing.T, root string) {
		if code, out, _ := hook("pre-tool-use", payload(t, "pre-tool-use-write-main-paired", root, nil)); code != 0 || out != nil {
			t.Fatalf("allowed write: got %d %v", code, out)
		}
		if p, ok := readPending(t, root, "toolu_fixture_main_write"); !ok || p.Path != "new.txt" || p.Pre != nil {
			t.Fatalf("pending for a new file: %+v %v, want pre null", p, ok)
		}
	}},
	{"denied edit pends nothing", func(t *testing.T, root string) {
		in := payload(t, "pre-tool-use-edit-main-paired", root, toolFile(filepath.Join(root, ".assure", "waivers.yaml")))
		if code, _, _ := hook("pre-tool-use", in); code != 2 {
			t.Fatalf("protected edit not denied: %d", code)
		}
		noPendingState(t, root)
	}},
	{"invalid tool_use_id pends nothing and still allows", func(t *testing.T, root string) {
		for _, id := range []string{"../escape", "", "a/b", strings.Repeat("x", 129)} {
			in := payload(t, "pre-tool-use-edit-main-paired", root, func(m map[string]any) { m["tool_use_id"] = id })
			if code, out, _ := hook("pre-tool-use", in); code != 0 || out != nil {
				t.Fatalf("tool_use_id %q: the pending write must not change the allow: %d %v", id, code, out)
			}
		}
		noPendingState(t, root)
	}},
	{"session start prunes only old orphans", func(t *testing.T, root string) {
		old := app.PendingPath(root, "other-session", "toolu_old")
		live := app.PendingPath(root, "other-session", "toolu_live")
		for _, p := range []string{old, live} {
			writeFile(t, root, strings.TrimPrefix(p, root+"/"), `{"v":0,"path":"notes.txt","pre":null}`)
		}
		stale := time.Now().Add(-15 * 24 * time.Hour)
		if err := os.Chtimes(old, stale, stale); err != nil {
			t.Fatal(err)
		}
		hook("session-start", payload(t, "session-start-startup", root, nil))
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("15-day-old orphan pending entry survived SessionStart: %v", err)
		}
		if _, err := os.Stat(live); err != nil {
			t.Errorf("another live session's pending entry was removed: %v", err)
		}
	}},
}

func TestPendingEntries(t *testing.T) {
	for _, s := range pendingScenarios {
		t.Run(s.name, func(t *testing.T) { s.run(t, gitRepo(t)) })
	}
}

func bash(m map[string]any) {
	m["tool_name"] = "Bash"
	m["tool_input"] = map[string]any{"command": "echo hi > notes.txt"}
}

var postToolUseScenarios = []struct {
	name string
	run  func(t *testing.T, root string)
}{
	{"subagent edit carries agent fields", func(t *testing.T, root string) {
		pre := gitOut(t, root, "hash-object", "notes.txt")
		if code, out, _ := hook("pre-tool-use", payload(t, "pre-tool-use-edit-subagent-paired", root, nil)); code != 0 || out != nil {
			t.Fatalf("pre: %d %v", code, out)
		}
		writeFile(t, root, "notes.txt", "hello\n")
		if code, out, _ := hook("post-tool-use", payload(t, "post-tool-use-edit-subagent", root, nil)); code != 0 || out != nil {
			t.Fatalf("post: %d %v", code, out)
		}
		recs := provenanceLines(t, root)
		want := map[string]any{"v": 0.0, "session": session, "agent_id": "a0000000000000001", "agent_type": "general-purpose",
			"tool": "Edit", "path": "notes.txt", "role": "unclassified", "pre": pre, "post": gitOut(t, root, "hash-object", "notes.txt")}
		if len(recs) != 1 || len(recs[0]) != len(want) {
			t.Fatalf("records %v, want exactly %v", recs, want)
		}
		for k, v := range want {
			if recs[0][k] != v {
				t.Errorf("%s = %v, want %v", k, recs[0][k], v)
			}
		}
		if _, ok := readPending(t, root, "toolu_fixture_sub_edit"); ok {
			t.Error("pending entry survived its record")
		}
	}},
	{"main-thread record has no agent fields", func(t *testing.T, root string) {
		hook("pre-tool-use", payload(t, "pre-tool-use-write-main-paired", root, nil))
		writeFile(t, root, "new.txt", "new")
		if code, out, _ := hook("post-tool-use", payload(t, "post-tool-use-write-main", root, nil)); code != 0 || out != nil {
			t.Fatalf("post: %d %v", code, out)
		}
		recs := provenanceLines(t, root)
		if len(recs) != 1 || recs[0]["pre"] != nil || recs[0]["path"] != "new.txt" {
			t.Fatalf("records %v", recs)
		}
		for _, k := range []string{"agent_id", "agent_type"} {
			if _, ok := recs[0][k]; ok {
				t.Errorf("main-thread record carries %s: absence is what marks the main thread", k)
			}
		}
	}},
	{"no pending entry warns and appends nothing", func(t *testing.T, root string) {
		writeFile(t, root, "notes.txt", "hello\n")
		code, out, _ := hook("post-tool-use", payload(t, "post-tool-use-edit-subagent", root, nil))
		msg, _ := out["systemMessage"].(string)
		if code != 0 || len(out) != 1 || !strings.Contains(msg, "notes.txt") || !strings.Contains(msg, "gap") {
			t.Fatalf("got %d %v; PostToolUse must never block, and must name the path", code, out)
		}
		if recs := provenanceLines(t, root); recs != nil {
			t.Fatalf("appended without a pending entry: %v", recs)
		}
	}},
	{"Bash is not recorded", func(t *testing.T, root string) {
		if code, out, _ := hook("pre-tool-use", payload(t, "pre-tool-use-edit-subagent-paired", root, bash)); code != 0 || out != nil {
			t.Fatalf("pre Bash: %d %v", code, out)
		}
		writeFile(t, root, "notes.txt", "hi\n")
		if code, out, _ := hook("post-tool-use", payload(t, "post-tool-use-edit-subagent", root, bash)); code != 0 || out != nil {
			t.Fatalf("post Bash: %d %v", code, out)
		}
		if recs := provenanceLines(t, root); recs != nil {
			t.Fatalf("Bash recorded: %v", recs)
		}
		noPendingState(t, root)
	}},
	{"recording is not drift", func(t *testing.T, root string) {
		writeFile(t, root, filepath.Join(core.ProvenanceDir, "earlier.jsonl"), "")
		hook("session-start", payload(t, "session-start-startup", root, nil))
		hook("pre-tool-use", payload(t, "pre-tool-use-edit-subagent-paired", root, nil))
		writeFile(t, root, "notes.txt", "hello\n")
		hook("post-tool-use", payload(t, "post-tool-use-edit-subagent", root, nil))
		if len(provenanceLines(t, root)) != 1 {
			t.Fatal("fixture recorded nothing, so drift has nothing to ignore")
		}
		m, err := core.LoadManifest(filepath.Join(root, "assurance.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if d, err := drift(m, session); err != nil || len(d) != 0 {
			t.Fatalf("record appends reported as drift: %v %v", d, err)
		}
		writeFile(t, root, ".assure/waivers.yaml", "[] # edited\n")
		if d, err := drift(m, session); err != nil || len(d) != 1 {
			t.Fatalf("a real protected change is still drift: %v %v", d, err)
		}
	}},
}

func TestPostToolUseRecords(t *testing.T) {
	for _, s := range postToolUseScenarios {
		t.Run(s.name, func(t *testing.T) { s.run(t, gitRepo(t)) })
	}
}

func FuzzPostToolUse(f *testing.F) {
	root := fixture(f)
	writeFile(f, root, "notes.txt", "hey\n")
	for _, name := range []string{"post-tool-use-edit-main", "post-tool-use-write-main", "post-tool-use-edit-subagent"} {
		f.Add(payload(f, name, root, nil))
	}
	f.Add([]byte(`{"hook_event_name":"PostToolUse","session_id":"s","cwd":"` + root + `","tool_name":"Edit","tool_use_id":"t","tool_input":{"file_path":"notes.txt"}}`))
	f.Add([]byte(`{"hook_event_name":`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var out, errb bytes.Buffer
		if code := Run("post-tool-use", bytes.NewReader(data), &out, &errb); code != 0 {
			t.Fatalf("post-tool-use exited %d: the tool already ran, so it must never block: %q", code, out.String())
		}
		if out.Len() == 0 {
			return
		}
		var o map[string]any
		if err := json.Unmarshal(out.Bytes(), &o); err != nil {
			t.Fatalf("output is not JSON: %q", out.String())
		}
		if _, ok := o["systemMessage"]; !ok || len(o) != 1 {
			t.Fatalf("post-tool-use output may only carry a systemMessage: %q", out.String())
		}
	})
}
