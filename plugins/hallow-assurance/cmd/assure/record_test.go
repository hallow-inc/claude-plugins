package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const recSession = "s1"

func recordRepo(t *testing.T) (repo, *core.Manifest) {
	t.Helper()
	r := newRepo(t, levelB+"provenance: true\n")
	r.write(".gitignore", ".assure/state/\n")
	r.write("p/p.go", "package p\n")
	r.write("p/p_test.go", "package p\n")
	r.git("init", "-q")
	r.git("add", "-A")
	r.git("commit", "-qm", "init")
	m, err := app.ManifestFor(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return r, m
}

func pend(t *testing.T, m *core.Manifest, session, toolUseID, rel string) {
	t.Helper()
	if err := app.WritePending(m, session, toolUseID, rel); err != nil {
		t.Fatal(err)
	}
}

func sessionLines(t *testing.T, r repo, session string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.root, core.ProvenanceDir, session+".jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.Provenance, data); err != nil || len(vs) > 0 {
		t.Fatalf("%s.jsonl violates provenance.schema.json: %v %v\n%s", session, err, vs, data)
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

func blobOf(t *testing.T, r repo, rel string) string {
	t.Helper()
	blobs, err := os.ReadFile(filepath.Join(r.root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return core.BlobHash(blobs)
}

var recordScenarios = []struct {
	name string
	run  func(t *testing.T, r repo, m *core.Manifest)
}{
	{"subagent edit", func(t *testing.T, r repo, m *core.Manifest) {
		pre := blobOf(t, r, "p/p_test.go")
		pend(t, m, recSession, "toolu_1", "p/p_test.go")
		r.write("p/p_test.go", "package p\n\n// edited\n")
		code, _, stderr := assure("record", "--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_1",
			"--agent-type", core.Verifier, "--agent-id", "a1")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		recs := sessionLines(t, r, recSession)
		want := map[string]any{"v": 0.0, "session": recSession, "agent_id": "a1", "agent_type": core.Verifier, "tool": "Edit",
			"path": "p/p_test.go", "role": "test", "pre": pre, "post": blobOf(t, r, "p/p_test.go")}
		if len(recs) != 1 || len(recs[0]) != len(want) {
			t.Fatalf("records %v, want exactly %v", recs, want)
		}
		for k, v := range want {
			if recs[0][k] != v {
				t.Errorf("%s = %v, want %v", k, recs[0][k], v)
			}
		}
		if _, err := os.Stat(app.PendingPath(r.root, recSession, "toolu_1")); !os.IsNotExist(err) {
			t.Errorf("pending entry not deleted: %v", err)
		}
	}},
	{"main-thread new file has no agent fields", func(t *testing.T, r repo, m *core.Manifest) {
		pend(t, m, recSession, "toolu_1", "p/new.go")
		r.write("p/new.go", "package p\n")
		if code, _, stderr := assure("record", "--session", recSession, "--tool", "Write", "--tool-use-id", "toolu_1"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		recs := sessionLines(t, r, recSession)
		if len(recs) != 1 || recs[0]["pre"] != nil || recs[0]["role"] != "source" {
			t.Fatalf("records %v", recs)
		}
		for _, k := range []string{"agent_id", "agent_type"} {
			if _, ok := recs[0][k]; ok {
				t.Errorf("main-thread record carries %s", k)
			}
		}
	}},
	{"deletion has null post", func(t *testing.T, r repo, m *core.Manifest) {
		pend(t, m, recSession, "toolu_1", "p/p.go")
		if err := os.Remove(filepath.Join(r.root, "p/p.go")); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := assure("record", "--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_1"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if recs := sessionLines(t, r, recSession); len(recs) != 1 || recs[0]["post"] != nil || recs[0]["pre"] == nil {
			t.Fatalf("records %v", recs)
		}
	}},
	{"missing pending exits 1", func(t *testing.T, r repo, _ *core.Manifest) {
		code, _, stderr := assure("record", "--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_missing_qz")
		if code != 1 || !strings.Contains(stderr, "toolu_missing_qz") {
			t.Fatalf("got %d %q", code, stderr)
		}
		if recs := sessionLines(t, r, recSession); recs != nil {
			t.Fatalf("appended without a pending entry: %v", recs)
		}
	}},
	{"no-op edit appends nothing", func(t *testing.T, r repo, m *core.Manifest) {
		pend(t, m, recSession, "toolu_1", "p/p.go")
		if code, _, stderr := assure("record", "--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_1"); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if recs := sessionLines(t, r, recSession); recs != nil {
			t.Fatalf("no-op edit appended: %v", recs)
		}
	}},
	{"invalid arguments exit 2 and write nothing", func(t *testing.T, r repo, m *core.Manifest) {
		pend(t, m, recSession, "toolu_1", "p/p.go")
		r.write("p/p.go", "package p\n\n// edited\n")
		for _, args := range [][]string{
			{"--session", "../s1", "--tool", "Edit", "--tool-use-id", "toolu_1"},
			{"--session", recSession, "--tool", "Edit", "--tool-use-id", "../s1/toolu_1"},
			{"--session", "", "--tool", "Edit", "--tool-use-id", "toolu_1"},
			{"--session", recSession, "--tool", "Edit", "--tool-use-id", strings.Repeat("t", 129)},
			{"--session", recSession, "--tool-use-id", "toolu_1"},
			{"--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_1", "extra"},
		} {
			if code, _, stderr := assure(append([]string{"record"}, args...)...); code != 2 || !strings.Contains(stderr, "usage") {
				t.Errorf("%v: got %d %q", args, code, stderr)
			}
		}
		if _, err := os.Stat(filepath.Join(r.root, core.ProvenanceDir)); !os.IsNotExist(err) {
			t.Fatalf("invalid arguments wrote provenance: %v", err)
		}
		if _, err := os.Stat(app.PendingPath(r.root, recSession, "toolu_1")); err != nil {
			t.Fatalf("invalid arguments consumed the pending entry: %v", err)
		}
	}},
	{"provenance off reads and writes nothing", func(t *testing.T, r repo, m *core.Manifest) {
		pend(t, m, recSession, "toolu_1", "p/p.go")
		r.write("p/p.go", "package p\n\n// edited\n")
		r.write("assurance.yaml", levelB)
		if code, _, stderr := assure("record", "--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_1"); code != 0 {
			t.Fatalf("valid ids with provenance off: exit %d %s", code, stderr)
		}
		if code, _, stderr := assure("record", "--session", recSession, "--tool", "Edit", "--tool-use-id", "toolu_missing"); code != 0 {
			t.Fatalf("provenance off must not look for a pending entry: exit %d %s", code, stderr)
		}
		if code, _, _ := assure("record", "--session", "../s1", "--tool", "Edit", "--tool-use-id", "toolu_1"); code != 2 {
			t.Fatalf("invalid ids with provenance off: exit %d, want 2", code)
		}
		if _, err := os.Stat(filepath.Join(r.root, core.ProvenanceDir)); !os.IsNotExist(err) {
			t.Fatalf("provenance off wrote under %s: %v", core.ProvenanceDir, err)
		}
		if _, err := os.Stat(app.PendingPath(r.root, recSession, "toolu_1")); err != nil {
			t.Fatalf("provenance off consumed the pending entry: %v", err)
		}
	}},
}

func TestRecordCommand(t *testing.T) {
	for _, s := range recordScenarios {
		t.Run(s.name, func(t *testing.T) {
			r, m := recordRepo(t)
			s.run(t, r, m)
		})
	}
}

func TestHookCommand(t *testing.T) {
	if code, out, _ := assure("hook", "protocol"); code != 0 || out != "2\n" {
		t.Fatalf("protocol: got %d %q; the plugin shim refuses any other number", code, out)
	}
	if code, _, stderr := assure("hook", "post-compact"); code != 2 || !strings.Contains(stderr, "post-tool-use") {
		t.Fatalf("unknown event: got %d %q", code, stderr)
	}
}

func TestParallelRecordsStayValid(t *testing.T) {
	r, m := recordRepo(t)
	iteration := 0
	rapid.Check(t, func(rt *rapid.T) {
		iteration++
		session := fmt.Sprintf("par%d", iteration)
		n := rapid.IntRange(2, 12).Draw(rt, "writers")
		agent := rapid.StringMatching(`[A-Za-z0-9:_-]{1,300}`)
		var args [][]string
		for i := range n {
			rel := fmt.Sprintf("q/f%d.go", rapid.IntRange(0, n-1).Draw(rt, "file"))
			id := fmt.Sprintf("toolu_%d", i)
			pend(t, m, session, id, rel)
			a := []string{"record", "--session", session, "--tool", "Edit", "--tool-use-id", id}
			if rapid.Bool().Draw(rt, "subagent") {
				a = append(a, "--agent-id", agent.Draw(rt, "id"), "--agent-type", agent.Draw(rt, "type"))
			}
			args = append(args, a)
		}
		for i := range n {
			r.write(fmt.Sprintf("q/f%d.go", i), fmt.Sprintf("package q\n\n// %s %d\n", session, i))
		}
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i, a := range args {
			wg.Go(func() { codes[i], _, _ = assure(a...) })
		}
		wg.Wait()
		for i, c := range codes {
			if c != 0 {
				rt.Fatalf("writer %d exited %d", i, c)
			}
		}
		if got := sessionLines(t, r, session); len(got) != n {
			rt.Fatalf("%d records for %d concurrent writers", len(got), n)
		}
	})
}
