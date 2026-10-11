package hallowassurance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const fakeAdapterXX = `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":1,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{}}' ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`

var waivedForIndependence = func() string {
	var b strings.Builder
	for _, id := range []string{"CODE-ZERO-WARNINGS", "CODE-RESOURCE-BOUNDS", "CODE-CHECK-RETURNS", "CODE-COMPLEXITY", "CODE-NO-UNSAFE",
		"VER-TESTS-PASS", "VER-MUTATION-CHANGED", "VER-FAIL-ON-BASE", "VER-TEST-BUDGET", "VER-ROBUST-FUZZ", "VER-COVERAGE-RESOLUTION"} {
		b.WriteString("- {objective: " + id + ", scope: '**', rationale: the fake adapter in this test produces no evidence, approver: owner, expires: 2099-01-01}\n")
	}
	return b.String()
}()

type provRepo struct {
	t    *testing.T
	root string
	bin  string
}

func newProvRepo(t *testing.T) provRepo {
	t.Helper()
	bin := buildBinaries(t)
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(fakeAdapterXX), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := provRepo{t, root, bin}
	r.write("assurance.yaml", "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\nprovenance: true\n")
	r.write(".gitignore", ".assure/state/\n")
	r.write(".assure/waivers.yaml", waivedForIndependence)
	r.write("p/x.xx", "one\n")
	r.git("init", "-q", "-b", "main")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	return r
}

func (r provRepo) write(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r provRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = r.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r provRepo) assure(stdin []byte, args ...string) (int, string, string) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), filepath.Join(r.bin, "assure"), args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "PATH="+r.bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func (r provRepo) payload(name, toolUseID, agentType, rel string) []byte {
	r.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(read(r.t, filepath.Join("testdata", "hooks", name+".json")), &m); err != nil {
		r.t.Fatal(err)
	}
	m["cwd"] = r.root
	m["tool_use_id"] = toolUseID
	m["agent_id"] = "agent-" + strings.TrimPrefix(agentType, "hallow-assurance:")
	m["agent_type"] = agentType
	m["tool_input"].(map[string]any)["file_path"] = filepath.Join(r.root, rel)
	data, err := json.Marshal(m)
	if err != nil {
		r.t.Fatal(err)
	}
	return data
}

// edit drives the real hook path for one Edit: PreToolUse as preAgent, the file write the tool
// would make, then PostToolUse as postAgent. They differ only where the guard would deny the
// recorded author; evaluate judges the record PostToolUse writes, whoever the guard saw.
func (r provRepo) edit(id, rel, body, preAgent, postAgent string) {
	r.t.Helper()
	if code, out, errb := r.assure(r.payload("pre-tool-use-edit-subagent-paired", id, preAgent, rel), "hook", "pre-tool-use"); code != 0 || out != "" {
		r.t.Fatalf("pre-tool-use %s as %s: exit %d %q %q", rel, preAgent, code, out, errb)
	}
	r.write(rel, body)
	if code, out, errb := r.assure(r.payload("post-tool-use-edit-subagent", id, postAgent, rel), "hook", "post-tool-use"); code != 0 || out != "" {
		r.t.Fatalf("post-tool-use %s as %s: exit %d %q %q", rel, postAgent, code, out, errb)
	}
}

type e2eEntry struct {
	Objective string   `json:"objective"`
	Status    string   `json:"status"`
	Details   []string `json:"details"`
}

func (r provRepo) evaluate(args ...string) (int, string, map[string]e2eEntry) {
	r.t.Helper()
	code, stdout, stderr := r.assure(nil, append([]string{"evaluate", "--changed-from", "main", "--date", "2026-10-01"}, args...)...)
	var rep struct {
		Problems   []string   `json:"problems"`
		Objectives []e2eEntry `json:"objectives"`
	}
	data, err := os.ReadFile(filepath.Join(r.root, ".assure/state/report.json"))
	if err != nil {
		r.t.Fatalf("evaluate exit %d wrote no report: %v\n%s\n%s", code, err, stdout, stderr)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		r.t.Fatal(err)
	}
	if len(rep.Problems) > 0 {
		r.t.Errorf("report problems: %v", rep.Problems)
	}
	byID := map[string]e2eEntry{}
	for _, e := range rep.Objectives {
		byID[e.Objective] = e
	}
	return code, stdout, byID
}

func acceptanceRepo(t *testing.T, testAuthor string) provRepo {
	t.Helper()
	r := newProvRepo(t)
	r.git("checkout", "-qb", "pr")
	r.edit("toolu_src", "p/x.xx", "two\n", core.Implementer, core.Implementer)
	r.edit("toolu_test", "p/x_test.xx", "check two\n", core.Verifier, testAuthor)
	r.git("add", "-A")
	r.git("commit", "-qm", "change")
	if st := r.git("status", "--porcelain"); st != "" {
		t.Fatalf("hook left untracked or modified files outside .assure/state: %s", st)
	}
	return r
}

func TestAcceptanceIndependence(t *testing.T) {
	for _, c := range []struct {
		testAuthor, status string
		code               int
	}{{core.Implementer, "fail", 1}, {core.Verifier, "pass", 0}} {
		t.Run(c.testAuthor, func(t *testing.T) {
			r := acceptanceRepo(t, c.testAuthor)
			code, stdout, rep := r.evaluate()
			ind := rep["IND-VERIFIER-DISTINCT"]
			if code != c.code || ind.Status != c.status {
				t.Fatalf("implementer wrote source, %s wrote test: exit %d IND %+v, want exit %d %s\n%s", c.testAuthor, code, ind, c.code, c.status, stdout)
			}
			named := false
			for _, d := range ind.Details {
				named = named || strings.HasPrefix(d, "p/x_test.xx: "+core.Implementer)
				if strings.HasPrefix(d, "p/x.xx:") || (c.code == 1 && !strings.HasPrefix(d, "p/x_test.xx:")) {
					t.Errorf("only the implementer-written test may fail: %s", d)
				}
			}
			if named != (c.code == 1) {
				t.Errorf("detail naming the test file and its implementer author: %v, want %v (%v)", named, c.code == 1, ind.Details)
			}
		})
	}
}
