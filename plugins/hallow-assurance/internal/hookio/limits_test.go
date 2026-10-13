package hookio

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"pgregory.net/rapid"
)

func TestHookInputLimitIsSixteenMiBInclusive(t *testing.T) {
	const limit = 16 << 20
	body := `{"hook_event_name":"Stop","cwd":"/x"}`
	padded := func(n int) []byte { return append([]byte(body), bytes.Repeat([]byte(" "), n-len(body))...) }
	in, err := parse(bytes.NewReader(padded(limit)))
	if err != nil || in.Cwd != "/x" {
		t.Fatalf("input of exactly %d bytes: cwd %q, err %v; a payload at the limit is legal, and refusing it fails the hook closed for no reason", limit, in.Cwd, err)
	}
	if _, err := parse(bytes.NewReader(padded(limit + 1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("input of %d bytes: err %v; an oversize payload must be refused as oversize, not read without bound", limit+1, err)
	}
}

func TestMessagesAreCappedAtNineThousandBytes(t *testing.T) {
	const limit = 9000
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.OneOf(rapid.IntRange(limit-2, limit+2), rapid.IntRange(0, 2*limit)).Draw(t, "len")
		s := strings.Repeat(rapid.SampledFrom([]string{"a", "Z", " ", "\n"}).Draw(t, "char"), n)
		got := capMessage(s)
		if n <= limit && got != s {
			t.Fatalf("message of %d bytes changed to %d bytes; a message within the limit must reach the human whole", n, len(got))
		}
		if want := s[:min(n, limit)] + "\n… truncated"; n > limit && got != want {
			t.Fatalf("message of %d bytes became %d bytes ending %q; an over-limit message must keep exactly the first %d bytes and say it was cut", n, len(got), got[max(0, len(got)-20):], limit)
		}
	})
}

func TestSessionStartWithoutWarningsIsExactlyTheContext(t *testing.T) {
	root := fixture(t)
	_, out, _ := hook("session-start", payload(t, "session-start-startup", root, nil))
	ctx, _ := out["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	m, err := core.LoadManifest(filepath.Join(root, "assurance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := core.RenderContext(m, nil); ctx != want {
		t.Fatalf("SessionStart context with nothing to warn about:\n%q\nwant exactly\n%q\nan empty warning preamble reads as a warning the agent cannot find", ctx, want)
	}
}

func TestStopSummaryHeadersOnlyIntroduceContent(t *testing.T) {
	const driftHeader, remedyHeader = "\nProtected-file drift:\n", "\nThe agent cannot fix these; a human must:\n"
	rapid.Check(t, func(t *rapid.T) {
		in := stopInputGen(t)
		s := in.summary()
		if want := len(in.drifted) > 0 || in.driftErr != nil; strings.Contains(s, driftHeader) != want {
			t.Fatalf("drift header present = %v, want %v (drifted %v, err %v):\n%s\nan empty header tells the human to review drift that does not exist", !want, want, in.drifted, in.driftErr, s)
		}
		if want := len(in.rep.Remedies()) > 0 || core.OutsideReach(in.driftErr); strings.Contains(s, remedyHeader) != want {
			t.Fatalf("remedy header present = %v, want %v:\n%s\nan empty header asks the human for an action nobody named", !want, want, s)
		}
	})
}
