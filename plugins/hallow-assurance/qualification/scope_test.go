package qualification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

// The statuses internal/core counts as undetected when it scores VER-MUTATION-CHANGED.
var undetectedStatus = map[string]bool{"Survived": true, "NoCoverage": true}

func runScopeCase(t *testing.T, c qcase) {
	t.Helper()
	p := prepareCase(t, c, filepath.Join(binDir, adapterName(c.lang)))
	got, log := scopedMutants(t, c.lang, p, c.scope)
	var lines []string
	for _, m := range got {
		if l := fmt.Sprintf("%s:%d", m.Path, m.Line); undetectedStatus[m.Status] && !slices.Contains(lines, l) {
			lines = append(lines, l)
		}
	}
	slices.Sort(lines)
	if want := slices.Sorted(slices.Values(c.survivors)); !slices.Equal(lines, want) {
		t.Errorf("surviving mutant lines %q, want exactly %q: scope ranges limit mutation evidence to the lines they name\n%s", lines, want, log)
	}
	whole := make(adapterkit.Scope, len(c.scope))
	for i, e := range c.scope {
		whole[i] = adapterkit.ScopeEntry{Path: e.Path}
	}
	all, wholeLog := scopedMutants(t, c.lang, p, whole)
	var outside []string
	for _, m := range all {
		if undetectedStatus[m.Status] && !c.scope.Contains(m.Path, m.Line) {
			outside = append(outside, fmt.Sprintf("%s:%d", m.Path, m.Line))
		}
	}
	if len(outside) == 0 {
		t.Errorf("with ranges stripped, no mutant survives outside the scope's ranges; the fixture proves nothing without an outside survivor, because an adapter that ignores ranges would pass it too\n%s", wholeLog)
	}
}

func scopedMutants(t *testing.T, lang string, p preparedCase, s adapterkit.Scope) ([]evidence.Mutant, string) {
	t.Helper()
	scope := filepath.Join(t.TempDir(), "scope.json")
	if err := os.WriteFile(scope, scopeJSON(s), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(realTempDir(t), "ev")
	cmd := exec.CommandContext(t.Context(), p.tools[adapterName(lang)], "run", "VER-MUTATION-CHANGED", "--scope", scope, "--out", out)
	cmd.Dir = p.root
	cmd.Env = append(os.Environ(), "PATH="+p.path, "RAPID_SEED=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s run VER-MUTATION-CHANGED --scope %s: %v\nstdout:\n%s\nstderr:\n%s", adapterName(lang), scopeJSON(s), err, stdout.String(), stderr.String())
	}
	log := fmt.Sprintf("scope %s\nresponse:\n%s\nstderr:\n%s", scopeJSON(s), stdout.String(), stderr.String())
	if vs, err := schemas.Validate(schemas.AdapterRun, stdout.Bytes()); err != nil || len(vs) > 0 {
		t.Fatalf("run response invalid: %v %v\n%s", err, vs, log)
	}
	var resp struct {
		Evidence []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"evidence"`
		Environment json.RawMessage `json:"environment"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Environment != nil || len(resp.Evidence) != 1 || resp.Evidence[0].Type != "mutation.report" {
		t.Fatalf("want one mutation.report evidence file and no environment error\n%s", log)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := evidence.ParseMutationReport(data)
	if err != nil {
		t.Fatal(err)
	}
	return ms, log + "\nevidence:\n" + string(data)
}
