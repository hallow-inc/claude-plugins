package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

var snapshotPool = map[string]bool{
	"assurance.yaml":              true,
	".assure/waivers.yaml":        true,
	".assure/baseline/lint.json":  true,
	".assure/state/adapters.json": false,
	"secret/a.txt":                true,
	"secret/deep/b.txt":           true,
	".claude/settings.json":       true,
	"src/x.go":                    false,
	"notes.md":                    false,
	".git/config":                 false,
}

func poolPaths() []string {
	var out []string
	for p := range snapshotPool {
		out = append(out, p)
	}
	return out
}

func snapshotManifest(t interface{ Fatalf(string, ...any) }, root string) (*Manifest, []Glob) {
	prot, err := CompileGlob("secret/**")
	if err != nil {
		t.Fatalf("%v", err)
	}
	extra, err := CompileGlob(".claude/settings.json")
	if err != nil {
		t.Fatalf("%v", err)
	}
	return &Manifest{Root: root, DefaultLevel: "B", Protected: []Glob{prot}}, []Glob{extra}
}

func writeFile(t interface{ Fatalf(string, ...any) }, root, rel, body string) {
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestDriftReportsExactlyProtectedChanges(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "snap")
		if err != nil {
			t.Fatalf("%v", err)
		}
		defer func() { _ = os.RemoveAll(root) }()
		m, extra := snapshotManifest(t, root)
		paths := rapid.Permutation(poolPaths()).Draw(t, "order")
		exists := map[string]bool{}
		for _, p := range paths {
			if rapid.Bool().Draw(t, "exists "+p) {
				writeFile(t, root, p, rapid.String().Draw(t, "body"))
				exists[p] = true
			}
		}
		snap, err := TakeSnapshot(m, "s1", extra)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		target := rapid.SampledFrom(paths).Draw(t, "target")
		full := filepath.Join(root, target)
		switch {
		case !exists[target]:
			writeFile(t, root, target, "new")
		case rapid.Bool().Draw(t, "delete"):
			if err := os.Remove(full); err != nil {
				t.Fatalf("%v", err)
			}
		default:
			data, _ := os.ReadFile(full)
			writeFile(t, root, target, string(data)+"!")
		}
		drift, err := Drift(m, snap, extra)
		if err != nil {
			t.Fatalf("drift: %v", err)
		}
		if protected := snapshotPool[target]; protected != (len(drift) == 1 && strings.HasSuffix(drift[0], " "+target)) || (!protected && len(drift) > 0) {
			t.Fatalf("mutating %s (protected=%v) gave drift %v", target, protected, drift)
		}
	})
}

func TestSnapshotHashesMatchGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	body := "version: 0\n\x00binary"
	out, err := exec.Command("sh", "-c", "printf 'version: 0\\n\\000binary' | git hash-object --stdin").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := BlobHash([]byte(body)); got != strings.TrimSpace(string(out)) {
		t.Fatalf("BlobHash %s, git %s", got, out)
	}
}

func TestSnapshotRoundTripsThroughSchema(t *testing.T) {
	root := t.TempDir()
	m, extra := snapshotManifest(t, root)
	writeFile(t, root, "assurance.yaml", "x")
	writeFile(t, root, "secret/a.txt", "y")
	s, err := TakeSnapshot(m, "abc_1-2", extra)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(m, s); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(m, "abc_1-2")
	if err != nil || len(got.Files) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := ReadSnapshot(m, "other"); err == nil {
		t.Fatal("missing snapshot read without error")
	}
}

func TestInvalidSessionIDs(t *testing.T) {
	for _, id := range []string{"", "a/b", "..", "a b", strings.Repeat("a", 129)} {
		if ValidSession(id) {
			t.Errorf("%q accepted", id)
		}
	}
	if !ValidSession("00000000-0000-4000-8000-000000000001") {
		t.Error("Claude Code session id rejected")
	}
}

func TestPruneStateRemovesOnlyOldSessionFiles(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"snapshot-old.json", "stop-old.json", "snapshot-new.json", "adapters.json"} {
		writeFile(t, root, filepath.Join(StateDir, f), "{}")
	}
	old := time.Now().Add(-15 * 24 * time.Hour)
	for _, f := range []string{"snapshot-old.json", "stop-old.json", "adapters.json"} {
		if err := os.Chtimes(filepath.Join(root, StateDir, f), old, old); err != nil {
			t.Fatal(err)
		}
	}
	PruneState(root, 14*24*time.Hour, time.Now())
	for f, want := range map[string]bool{"snapshot-old.json": false, "stop-old.json": false, "snapshot-new.json": true, "adapters.json": true} {
		if _, err := os.Stat(filepath.Join(root, StateDir, f)); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", f, err == nil, want)
		}
	}
}
