package adapterproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func argRecordingAdapter(t *testing.T) (argv, resp, stderr string) {
	t.Helper()
	d := t.TempDir()
	argv, resp, stderr = filepath.Join(d, "argv"), filepath.Join(d, "response"), filepath.Join(d, "stderr")
	fakeAdapter(t, "go", `printf '%s\0' "$@" > '`+argv+`'
if [ -f '`+stderr+`' ]; then cat '`+stderr+`' >&2; exit 3; fi
cat '`+resp+`'`)
	return argv, resp, stderr
}

func recordedArgv(t *rapid.T, file string) []string {
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

func argWord() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-zA-Z0-9 ._/-]{1,12}`).Filter(func(s string) bool { return !strings.HasPrefix(s, "-") })
}

func runResponse(t *rapid.T) []byte {
	switch rapid.IntRange(0, 9).Draw(t, "shape") {
	case 0:
		return []byte("not json")
	case 1:
		return nil
	}
	tools := rapid.SliceOfDistinct(rapid.SampledFrom(pinTools), rapid.ID[string]).Draw(t, "tools")
	versions := map[string]string{"go": "go1.26.7"}
	pinned := map[string]string{}
	for _, tool := range tools {
		if rapid.IntRange(0, 5).Draw(t, "versioned "+tool) > 0 {
			versions[tool] = "1.2.3"
		}
		pinned[tool] = rapid.SampledFrom([]string{"default", "mise.toml"}).Draw(t, "pin "+tool)
	}
	evidence := []map[string]string{}
	for _, p := range rapid.SliceOfN(rapid.SampledFrom([]string{"junit.xml", "m/report.json", "../x", "/abs"}), 0, 2).Draw(t, "evidence") {
		evidence = append(evidence, map[string]string{"type": rapid.SampledFrom([]string{"test.junit", "mutation.report", "fuzz.run", "coverage.resolution"}).Draw(t, "type"), "path": p})
	}
	body := map[string]any{
		"protocol":      rapid.SampledFrom([]int{2, 2, 2, 1, 3}).Draw(t, "protocol"),
		"evidence":      evidence,
		"tool_versions": versions,
		"pinned_by":     pinned,
	}
	if u := rapid.SliceOfNDistinct(rapid.SampledFrom(pinTools), 0, 2, rapid.ID[string]).Draw(t, "untracked"); len(u) > 0 {
		body["untracked"] = u
	}
	if rapid.IntRange(0, 4).Draw(t, "environment") == 0 {
		body["environment"] = map[string]string{"message": "gremlins is not on PATH"}
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return data
}

func TestRunObjectiveScopeSendsScopeNeverChangedFromAndValidatesLikeRunObjective(t *testing.T) {
	argv, resp, stderr := argRecordingAdapter(t)
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		obj := rapid.SampledFrom([]string{"VER-TESTS-PASS", "VER-MUTATION-CHANGED", "VER-COVERAGE-RESOLUTION", "VER-ROBUST-FUZZ"}).Draw(t, "objective")
		scope := argWord().Draw(t, "scope")
		out := argWord().Draw(t, "out")
		body := runResponse(t)
		if err := os.WriteFile(resp, body, 0o644); err != nil {
			t.Fatalf("%v", err)
		}
		_ = os.Remove(stderr)
		if rapid.IntRange(0, 9).Draw(t, "crash") == 0 {
			if err := os.WriteFile(stderr, []byte("adapter-broke"), 0o644); err != nil {
				t.Fatalf("%v", err)
			}
		}
		base, baseErr := RunObjective(dir, "go", obj, "main", out, 5*time.Second)
		got, err := RunObjectiveScope(dir, "go", obj, scope, out, 5*time.Second)
		if want := []string{"run", obj, "--scope", scope, "--out", out}; !slices.Equal(recordedArgv(t, argv), want) {
			t.Fatalf("adapter argv %q, want %q; an audit has no base commit, so the adapter must get --scope and never --changed-from, or it runs against a diff instead of the named code", recordedArgv(t, argv), want)
		}
		if (err == nil) != (baseErr == nil) || (err != nil && err.Error() != baseErr.Error()) {
			t.Fatalf("response %s: RunObjectiveScope error %v, RunObjective error %v; a scoped run must apply the same protocol checks, or audit trusts a response evaluate would reject", body, err, baseErr)
		}
		if err != nil {
			if ae, ok := errors.AsType[*Error](err); !ok || ae.Adapter != "assure-adapter-go" || ae.Sub != "run" {
				t.Fatalf("error %v does not name assure-adapter-go run", err)
			}
			return
		}
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("response %s: RunObjectiveScope = %+v, RunObjective = %+v; the same response must decode the same way, environment included", body, got, base)
		}
		if bytes.Contains(body, []byte(`"environment"`)) && got.Environment == nil {
			t.Fatalf("response %s: environment dropped; audit reports the language as not audited only if the client surfaces it", body)
		}
	})
}

func TestRunObjectiveScopeMissingAdapterIsUnstartable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := RunObjectiveScope(t.TempDir(), "go", "VER-TESTS-PASS", "scope.json", "out", time.Second)
	if !errors.Is(err, core.ErrAdapterUnstartable) || !strings.Contains(err.Error(), "assure-adapter-go") {
		t.Fatalf("got %v; a missing adapter must be named and classed unstartable so audit can list the language as not audited", err)
	}
}

func TestRunObjectiveScopeTimeoutKillsTheAdapter(t *testing.T) {
	fakeAdapter(t, "go", "exec sleep 10")
	start := time.Now()
	_, err := RunObjectiveScope(t.TempDir(), "go", "VER-MUTATION-CHANGED", "scope.json", "out", time.Second)
	if err == nil || !strings.Contains(err.Error(), "killed after timeout of 1s") || time.Since(start) > 5*time.Second {
		t.Fatalf("got %v after %s; a scoped mutation run over a large scope must still be bounded by the timeout", err, time.Since(start))
	}
}
