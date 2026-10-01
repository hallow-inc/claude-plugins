package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

func fpPool(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%064x", i)
	}
	return out
}

func failing(fp string) StopCheck { return StopCheck{Fingerprint: fp} }

func stopViolation(c StopCheck, active bool, action StopAction, s StopState) string {
	switch {
	case !active && action == StopBlock && s.Blocks != 1:
		return fmt.Sprintf("an inactive stop hook did not reset the count: %+v", s)
	case c.Passed && (action != StopAllow || s.Blocks != 0 || len(s.Fingerprints) != 0):
		return fmt.Sprintf("pass gave %v with %+v", action, s)
	case !c.Passed && action == StopAllow:
		return "failing check allowed without a message"
	case c.OutsideReach && !c.Passed && action == StopBlock:
		return "a failure outside the agent's reach blocked"
	}
	return ""
}

func TestStopBlocksAreBoundedAndResets(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pool := fpPool(rapid.IntRange(1, 4).Draw(t, "pool"))
		var s StopState
		last, run := StopAllow, 0
		for i := range rapid.IntRange(1, 30).Draw(t, "stops") {
			active := last == StopBlock || rapid.Bool().Draw(t, "active")
			c := StopCheck{
				Passed:       rapid.Bool().Draw(t, "passed"),
				OutsideReach: rapid.IntRange(0, 5).Draw(t, "outside") == 0,
				Fingerprint:  rapid.SampledFrom(pool).Draw(t, "fp"),
			}
			var action StopAction
			s, action = NextStop(s, active, c)
			if msg := stopViolation(c, active, action, s); msg != "" {
				t.Fatalf("stop %d: %s", i, msg)
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

func TestTotalBlocksPerFingerprintAreBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pool := fpPool(rapid.SampledFrom([]int{1, 2, 3, 4, maxFingerprints}).Draw(t, "pool"))
		var s StopState
		last := StopAllow
		blocks := map[string]int{}
		for i := range rapid.IntRange(1, 200).Draw(t, "stops") {
			active := last == StopBlock || rapid.Bool().Draw(t, "active")
			fp := rapid.SampledFrom(pool).Draw(t, "fp")
			s, last = NextStop(s, active, failing(fp))
			if last == StopBlock {
				blocks[fp]++
			}
			if blocks[fp] > StopCap {
				t.Fatalf("stop %d: fingerprint %s blocked %d times", i, fp[60:], blocks[fp])
			}
		}
	})
}

func TestEscalationAfterThreeBlocks(t *testing.T) {
	var s StopState
	var got []StopAction
	for i := range 4 {
		var a StopAction
		s, a = NextStop(s, i > 0, failing("a"))
		got = append(got, a)
	}
	want := []StopAction{StopBlock, StopBlock, StopBlock, StopEscalate}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestEscalatedFailureDoesNotBlockAfterNewPrompt(t *testing.T) {
	var s StopState
	for i := range StopCap + 1 {
		s, _ = NextStop(s, i > 0, failing("a"))
	}
	if _, a := NextStop(s, false, failing("a")); a == StopBlock {
		t.Fatal("an unchanged, already escalated failure blocked again after a new prompt")
	}
	if _, a := NextStop(s, false, failing("b")); a != StopBlock {
		t.Fatalf("a new failure after an escalation got %v, want a block", a)
	}
}

func TestCorruptStopStateReadsAsZero(t *testing.T) {
	root := t.TempDir()
	want := StopState{Version: stopStateVersion, Blocks: 2, Fingerprints: []FingerprintBlocks{{Fingerprint: fpPool(1)[0], Blocks: 3}}}
	if err := WriteStopState(root, "s", want); err != nil {
		t.Fatal(err)
	}
	if got := ReadStopState(root, "s"); got.Blocks != 2 || len(got.Fingerprints) != 1 || got.Fingerprints[0] != want.Fingerprints[0] {
		t.Fatalf("round trip got %+v", got)
	}
	for _, body := range []string{"{", `{"version":1,"blocks":-4}`, `{"version":1}`, `{"version":0,"blocks":2}`} {
		if err := os.WriteFile(filepath.Join(root, StateDir, "stop-s.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ReadStopState(root, "s"); got.Blocks != 0 || got.Fingerprints != nil {
			t.Fatalf("%q read as %+v", body, got)
		}
	}
}

func TestFingerprintListDropsOldestPastCap(t *testing.T) {
	pool := fpPool(maxFingerprints + 1)
	var s StopState
	for _, fp := range pool {
		s, _ = NextStop(s, false, failing(fp))
	}
	if len(s.Fingerprints) != maxFingerprints || s.Fingerprints[0].Fingerprint != pool[1] {
		t.Fatalf("kept %d, first %s", len(s.Fingerprints), s.Fingerprints[0].Fingerprint[60:])
	}
	root := t.TempDir()
	if err := WriteStopState(root, "s", s); err != nil {
		t.Fatal(err)
	}
	if got := ReadStopState(root, "s"); len(got.Fingerprints) != maxFingerprints {
		t.Fatalf("a full list did not round-trip through the schema: %d entries", len(got.Fingerprints))
	}
}

func TestFingerprintListKeepsEverythingAtExactlyTheCap(t *testing.T) {
	pool := fpPool(maxFingerprints)
	var s StopState
	for _, fp := range pool {
		s, _ = NextStop(s, false, failing(fp))
	}
	if len(s.Fingerprints) != maxFingerprints || s.Fingerprints[0].Fingerprint != pool[0] {
		t.Fatalf("a list at exactly the cap lost its oldest entry: kept %d, first %s", len(s.Fingerprints), s.Fingerprints[0].Fingerprint[60:])
	}
}

func TestBlockBudgetEndsExactlyAtStopCap(t *testing.T) {
	fp := fpPool(1)[0]
	s := StopState{Version: stopStateVersion, Fingerprints: []FingerprintBlocks{{Fingerprint: fp, Blocks: StopCap - 1}}}
	next, a := NextStop(s, false, failing(fp))
	if a != StopBlock || next.blocksFor(fp) != StopCap {
		t.Fatalf("one block left: got %v with %d recorded, want a block recording %d", a, next.blocksFor(fp), StopCap)
	}
	s = StopState{Version: stopStateVersion, Fingerprints: []FingerprintBlocks{{Fingerprint: fp, Blocks: StopCap}}}
	if _, a := NextStop(s, false, failing(fp)); a != StopEscalate {
		t.Fatalf("a spent budget got %v, want an escalation", a)
	}
}
