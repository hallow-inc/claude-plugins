package hookio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"pgregory.net/rapid"
)

const unbornManifest = "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n"

type unbornTB interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

func unbornWrite(t unbornTB, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func unbornGit(ctx context.Context, t unbornTB, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func unbornRepo(ctx context.Context, t unbornTB, parent string, files map[string]string) (string, *core.Manifest) {
	t.Helper()
	root, err := os.MkdirTemp(parent, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	unbornWrite(t, root, "assurance.yaml", unbornManifest)
	unbornWrite(t, root, ".assure/waivers.yaml", "[]\n")
	unbornWrite(t, root, ".gitignore", ".assure/state/\nign/\n")
	for rel, body := range files {
		unbornWrite(t, root, rel, body)
	}
	unbornGit(ctx, t, root, "init", "-q")
	m, err := app.ManifestFor(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, m
}

func unbornAdapter(t *testing.T, log string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":0,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{"VER-TESTS-PASS":{"tool":"fake","fast":true}}}' ;;
run)
  echo "$4" >> '` + log + `'
  mkdir -p "$6"
  printf '%s' '<testsuites><testsuite name="xx" tests="1"><testcase name="TestXX" classname="xx"/></testsuite></testsuites>' > "$6/junit.xml"
  printf '%s' '{"protocol":0,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"fake":"1"}}' ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

func unbornCheck(ctx context.Context, rt *rapid.T, parent, log string) {
	names := rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z]{1,6}`), 1, 5, rapid.ID[string]).Draw(rt, "names")
	ignored := rapid.SliceOfDistinct(rapid.StringMatching(`[a-z]{1,6}\.xx`), rapid.ID[string]).Draw(rt, "ignored")
	files := map[string]string{"p/" + names[0] + ".xx": "one\n"}
	for _, n := range names[1:] {
		files["q/"+n+rapid.SampledFrom([]string{".xx", ".txt"}).Draw(rt, "ext")] = "two\n"
	}
	want := len(files) + 3
	for _, n := range ignored {
		files["ign/"+n] = "ignored\n"
	}
	root, m := unbornRepo(ctx, rt, parent, files)
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		rt.Fatal(err)
	}
	rep := app.FastCheck(m, "HEAD", "cli")
	for _, res := range rep.Results {
		if res.ID == "changed-files" || strings.Contains(strings.Join(res.Details, "\n"), "bad revision") {
			rt.Fatalf("unborn HEAD reported as a failure: %s %s %v", res.ID, res.Status, res.Details)
		}
	}
	if rep.Changed != want {
		rt.Fatalf("changed %d files, want every unignored file (%d): %+v", rep.Changed, want, rep.Results)
	}
	if rep.Blocking() || !strings.Contains(rep.Render(), "pass\tVER-TESTS-PASS\txx") {
		rt.Fatalf("fast objectives did not run and pass on the unborn tree:\n%s", rep.Render())
	}
	data, _ := os.ReadFile(log)
	tree := unbornGit(ctx, rt, root, "hash-object", "-t", "tree", "/dev/null")
	refs := strings.Fields(string(data))
	if len(refs) == 0 {
		rt.Fatal("adapter run never invoked")
	}
	for _, ref := range refs {
		if ref != tree {
			rt.Fatalf("adapter got --changed-from %q, want the empty tree %q", ref, tree)
		}
	}
}

func TestFastCheckUnbornHeadDiffsAgainstEmptyTree(t *testing.T) {
	log := filepath.Join(t.TempDir(), "runs")
	unbornAdapter(t, log)
	parent := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) { unbornCheck(t.Context(), rt, parent, log) })

	root, m := unbornRepo(t.Context(), t, parent, map[string]string{"p/x.xx": "one\n"})
	hook("session-start", payload(t, "session-start-startup", root, nil))
	if code, out := stopWith(t, root, false); code != 0 || out != nil {
		t.Fatalf("Stop in an unborn repo: exit %d %v", code, out)
	}

	unbornGit(t.Context(), t, root, "commit", "-qm", "base", "--allow-empty")
	rep := app.FastCheck(m, "no-such-ref", "cli")
	if !rep.Blocking() || !strings.Contains(fmt.Sprint(rep.Failures(), rep.Render()), "no-such-ref") {
		t.Fatalf("bad ref did not fail with the git error:\n%s", rep.Render())
	}
}
