package core

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
)

var (
	ErrSnapshotMissing    = errors.New("protected-file snapshot missing or invalid")
	ErrAdapterUnstartable = errors.New("adapter cannot be started")
)

func OutsideReach(err error) bool {
	return errors.Is(err, ErrSnapshotMissing) || errors.Is(err, ErrAdapterUnstartable)
}

func (ev Evidence) Keys() []string {
	var keys []string
	for range ev.Problems {
		keys = append(keys, "problem")
	}
	for _, f := range ev.Failing {
		keys = append(keys, "test:"+f.Name)
	}
	for _, f := range ev.Findings {
		if f.Located {
			keys = append(keys, "finding:"+f.Path+":"+f.Rule)
		} else {
			keys = append(keys, "finding:"+f.Rule)
		}
	}
	for _, m := range ev.Mutants {
		switch classify(m.Status) {
		case undetected:
			keys = append(keys, "mutant:"+m.Path+":"+m.Mutator)
		case pending:
			keys = append(keys, "mutant-pending:"+m.Path+":"+m.Mutator)
		}
	}
	if b := ev.Base; b != nil {
		if b.Failed+len(b.Passed)+len(b.Skipped)+len(b.Errored) == 0 {
			keys = append(keys, "base-no-test")
		}
		for _, n := range b.Passed {
			keys = append(keys, "base-pass:"+n)
		}
		for _, n := range b.Skipped {
			keys = append(keys, "base-skip:"+n)
		}
		if len(b.Errored) > 0 {
			keys = append(keys, "base-error")
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

type FailureKey struct {
	Objective string
	Lang      string
	Keys      []string
}

func sortedSet(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return slices.Compact(s)
}

func StopFingerprint(failures []FailureKey, drifted []string) string {
	fs := make([]FailureKey, len(failures))
	for i, f := range failures {
		fs[i] = FailureKey{Objective: f.Objective, Lang: f.Lang, Keys: sortedSet(f.Keys)}
	}
	slices.SortFunc(fs, func(a, b FailureKey) int {
		return cmp.Or(cmp.Compare(a.Objective, b.Objective), cmp.Compare(a.Lang, b.Lang), slices.Compare(a.Keys, b.Keys))
	})
	fs = slices.CompactFunc(fs, func(a, b FailureKey) bool {
		return a.Objective == b.Objective && a.Lang == b.Lang && slices.Equal(a.Keys, b.Keys)
	})
	h := sha256.New()
	field := func(s string) { _, _ = fmt.Fprintf(h, "%d:%s", len(s), s) }
	for _, f := range fs {
		field("failure")
		field(f.Objective)
		field(f.Lang)
		field(fmt.Sprint(len(f.Keys)))
		for _, k := range f.Keys {
			field(k)
		}
	}
	for _, p := range sortedSet(drifted) {
		field("drifted")
		field(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
