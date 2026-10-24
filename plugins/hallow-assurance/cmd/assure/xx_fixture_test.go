package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

const xxManifest = "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\n"

var xxWaivers = func() string {
	var b strings.Builder
	for _, id := range []string{"VER-TESTS-PASS", "VER-MUTATION-CHANGED", "VER-FAIL-ON-BASE", "VER-ROBUST-FUZZ", "VER-COVERAGE-RESOLUTION"} {
		b.WriteString("- {objective: " + id + ", scope: '**', rationale: the fake adapter in this test produces no evidence, approver: owner, expires: 2099-01-01}\n")
	}
	return b.String()
}()

type xxFixture struct {
	repo
}

func xxAdapter(t *testing.T, objectives string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":2,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":` + objectives + `}' ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}

// xxBase commits a base on branch main, runs base to add more to it, then checks out branch pr.
func xxBase(t *testing.T, manifest string, base func(f xxFixture)) xxFixture {
	t.Helper()
	r := newRepo(t, manifest)
	xxAdapter(t, "{}")
	r.write(".gitignore", ".assure/state/\n")
	r.write(".assure/waivers.yaml", xxWaivers)
	r.write("p/x.xx", "one\n")
	r.write("p/x_test.xx", "check one\n")
	r.git("init", "-q", "-b", "main")
	f := xxFixture{r}
	if base != nil {
		base(f)
	}
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	r.git("checkout", "-qb", "pr")
	return f
}

func (f xxFixture) commit() {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-qm", "pr")
}

func (f xxFixture) head() string {
	f.t.Helper()
	out, err := exec.CommandContext(f.t.Context(), "git", "-C", f.root, "rev-parse", "HEAD").Output()
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func handEdit(f xxFixture) { f.write("p/x.xx", "hand edit\n") }

func detailWith(e app.Entry, parts ...string) bool {
	for _, d := range e.Details {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(d, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func rawReport(t *testing.T, r repo) map[string]json.RawMessage {
	t.Helper()
	readReport(t, r)
	data, err := os.ReadFile(filepath.Join(r.root, app.ReportFile))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
