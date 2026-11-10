package adapterkit

import "testing"

func init() { rerunUnderTest = Rerun }

func TestRerunRoundsIsTheProtocolConstant(t *testing.T) {
	if RerunRounds != protocolRounds {
		t.Fatalf("RerunRounds = %d, want %d; the protocol fixes the number of rerun rounds for every adapter", RerunRounds, protocolRounds)
	}
}
