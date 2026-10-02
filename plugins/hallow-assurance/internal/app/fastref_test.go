package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"pgregory.net/rapid"
)

func fastRefGit(t testing.TB, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fastRefPut(t testing.TB, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fastRefRepo(t testing.TB) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fastRefPut(t, root, "assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n")
	fastRefPut(t, root, ".assure/waivers.yaml", "[]\n")
	fastRefPut(t, root, ".gitignore", ".assure/state/\n")
	fastRefPut(t, root, "a.txt", "a\n")
	return root
}

func TestFastCheckMapsRefToEmptyTreeOnlyForUnbornHead(t *testing.T) {
	unborn := fastRefRepo(t)
	born := fastRefRepo(t)
	plain := fastRefRepo(t)
	fastRefGit(t, unborn, "init", "-q")
	fastRefGit(t, born, "init", "-q")
	fastRefGit(t, born, "add", "-A")
	fastRefGit(t, born, "commit", "-qm", "one")
	fastRefPut(t, born, "new.txt", "n\n")
	roots := map[string]string{"unborn": unborn, "born": born, "plain": plain}
	mans := map[string]*core.Manifest{}
	for k, r := range roots {
		m, err := app.ManifestFor(r)
		if err != nil {
			t.Fatal(err)
		}
		mans[k] = m
	}
	wantUnborn := len(strings.Fields(fastRefGit(t, unborn, "ls-files", "--others", "--exclude-standard")))

	rapid.Check(t, func(rt *rapid.T) {
		ref := rapid.OneOf(rapid.Just("HEAD"), rapid.StringMatching(`zz[A-Za-z0-9_]{0,8}`)).Draw(rt, "ref")
		state := rapid.SampledFrom([]string{"unborn", "born", "plain"}).Draw(rt, "state")
		rep := app.FastCheck(mans[state], ref, "fastref")
		var diffErr string
		for _, res := range rep.Results {
			if res.ID == "changed-files" {
				diffErr = strings.Join(res.Details, "\n")
			}
		}
		if ref == "HEAD" && state != "plain" {
			want := 1
			if state == "unborn" {
				want = wantUnborn
			}
			if diffErr != "" || rep.Changed != want {
				rt.Fatalf("%s repo, HEAD: changed %d (want %d), error %q; unborn HEAD diffs against the empty tree, a committed HEAD against itself", state, rep.Changed, want, diffErr)
			}
			return
		}
		if !strings.Contains(diffErr, "--relative "+ref+" --") {
			rt.Fatalf("%s repo, ref %q: error %q; the ref must reach git unchanged", state, ref, diffErr)
		}
	})
}
