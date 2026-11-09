package fuzzplant

import "testing"

func FuzzPlantedCrasher(f *testing.F) {
	f.Add("seed")
	f.Fuzz(func(t *testing.T, s string) {
		if s != "seed" {
			t.Fatalf("planted crasher: %q", s)
		}
	})
}
