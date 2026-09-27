package core

import (
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

func TestStopBlocksAreBoundedAndResets(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var s StopState
		last, run := StopAllow, 0
		for i := range rapid.IntRange(1, 30).Draw(t, "stops") {
			active := last == StopBlock || rapid.Bool().Draw(t, "active")
			passed := rapid.Bool().Draw(t, "passed")
			before := s
			var action StopAction
			s, action = NextStop(s, active, passed)
			if !active {
				if fresh, fa := NextStop(StopState{}, false, passed); fresh != s || fa != action {
					t.Fatalf("stop %d: an inactive stop hook did not reset the count (from %+v)", i, before)
				}
			}
			if passed && (action != StopAllow || s.Blocks != 0) {
				t.Fatalf("stop %d: pass gave %v with %+v", i, action, s)
			}
			if !passed && action == StopAllow {
				t.Fatalf("stop %d: failing check allowed without escalation", i)
			}
			if action == StopBlock {
				run++
			} else {
				run = 0
			}
			if run > StopCap {
				t.Fatalf("stop %d: %d consecutive blocks", i, run)
			}
			last = action
		}
	})
}

func TestEscalationAfterThreeBlocks(t *testing.T) {
	var s StopState
	var got []StopAction
	for i := range 4 {
		var a StopAction
		s, a = NextStop(s, i > 0, false)
		got = append(got, a)
	}
	want := []StopAction{StopBlock, StopBlock, StopBlock, StopEscalate}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCorruptStopStateReadsAsZero(t *testing.T) {
	root := t.TempDir()
	if err := WriteStopState(root, "s", StopState{Blocks: 2}); err != nil {
		t.Fatal(err)
	}
	if got := ReadStopState(root, "s"); got.Blocks != 2 {
		t.Fatalf("round trip got %+v", got)
	}
	for _, body := range []string{"{", `{"version":0,"blocks":-4}`, `{"version":0}`} {
		if err := os.WriteFile(filepath.Join(root, StateDir, "stop-s.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ReadStopState(root, "s"); got.Blocks != 0 {
			t.Fatalf("%q read as %+v", body, got)
		}
	}
}
