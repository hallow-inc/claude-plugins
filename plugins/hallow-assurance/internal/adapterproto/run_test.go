package adapterproto

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunObjectiveFailureModesNameTheAdapter(t *testing.T) {
	cases := map[string]struct{ script, want string }{
		"timeout":           {"exec sleep 10", "killed after timeout of 1s"},
		"invalid response":  {`echo '{"protocol":0,"evidence":[]}'`, "violates the protocol"},
		"unknown objective": {`echo "objective \"$2\" is not implemented" >&2; exit 2`, `objective "VER-X" is not implemented`},
		"escaping path":     {`echo '{"protocol":0,"evidence":[{"type":"test.junit","path":"../x"}],"tool_versions":{"go":"1"}}'`, "violates the protocol"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fakeAdapter(t, "go", c.script)
			_, err := RunObjective(t.TempDir(), "go", "VER-X", "HEAD", "out", time.Second)
			var ae *Error
			if !errors.As(err, &ae) || ae.Adapter != "assure-adapter-go" || ae.Sub != "run" {
				t.Fatalf("want *Error naming assure-adapter-go run, got %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestRunObjectivePassesProtocolArguments(t *testing.T) {
	fakeAdapter(t, "go", `[ "$1 $2 $3 $4 $5 $6" = "run VER-TESTS-PASS --changed-from main --out ev" ] || { echo "args: $*" >&2; exit 1; }
echo '{"protocol":0,"evidence":[{"type":"test.junit","path":"junit.xml"}],"tool_versions":{"go":"go1.26"}}'`)
	r, err := RunObjective(t.TempDir(), "go", "VER-TESTS-PASS", "main", "ev", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Evidence) != 1 || r.Evidence[0].Path != "junit.xml" || r.ToolVersions["go"] != "go1.26" {
		t.Fatalf("got %+v", r)
	}
}
