package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestMissingBaselineIsEmpty(t *testing.T) {
	b, err := LoadBaseline(t.TempDir())
	if err != nil || len(b.Entries) != 0 {
		t.Fatalf("got %+v, %v; a repo without a baseline must still evaluate", b, err)
	}
}

func TestDuplicateBaselineEntryNamesBoth(t *testing.T) {
	root := t.TempDir()
	entry := `{"objective":"CODE-ZERO-WARNINGS","rule":"r","path":"a.go","message":"m","count":1}`
	if err := os.MkdirAll(filepath.Join(root, ".assure"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, BaselineFile), []byte(`{"version":0,"entries":[`+entry+`,`+entry+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadBaseline(root)
	if err == nil || !strings.Contains(err.Error(), "entry 1 repeats entry 0") {
		t.Fatalf("got %v; two entries for one fingerprint make the count ambiguous", err)
	}
}

func finding(path, rule, msg string, line int) Finding {
	return Finding{Located: true, Path: path, Rule: rule, Message: msg, Text: path + ":" + string(rune('0'+line))}
}

func TestMovedFindingStaysBaselined(t *testing.T) {
	allowed := map[Fingerprint]int{{"CODE-ZERO-WARNINGS", "r", "a.go", "m"}: 1}
	kept, _ := matchBaseline(allowed, "CODE-ZERO-WARNINGS", []Finding{finding("a.go", "r", "m", 9)})
	if len(kept) != 0 {
		t.Fatalf("kept %+v; line numbers must not take part in the fingerprint", kept)
	}
}

func TestOneMoreThanBaselinedCounts(t *testing.T) {
	allowed := map[Fingerprint]int{{"CODE-ZERO-WARNINGS", "r", "a.go", "m"}: 2}
	fs := []Finding{finding("a.go", "r", "m", 1), finding("a.go", "r", "m", 2), finding("a.go", "r", "m", 3)}
	if kept, _ := matchBaseline(allowed, "CODE-ZERO-WARNINGS", fs); len(kept) != 1 {
		t.Fatalf("kept %d, want 1; the count stops a baselined message covering new copies", len(kept))
	}
}

func TestFixedFindingIsRemovable(t *testing.T) {
	b := Baseline{Entries: []BaselineEntry{{"CODE-ZERO-WARNINGS", "r", "a.go", "m", 2}}}
	_, used := matchBaseline(b.For("CODE-ZERO-WARNINGS"), "CODE-ZERO-WARNINGS", []Finding{finding("a.go", "r", "m", 1)})
	got := b.Removable(used, func(BaselineEntry) bool { return true })
	if len(got) != 1 || got[0].Unused != 1 {
		t.Fatalf("got %+v; a shrunk entry must be reported so the baseline can shrink", got)
	}
}

func TestUnlocatedFindingsNeverMatch(t *testing.T) {
	allowed := map[Fingerprint]int{{"CODE-ZERO-WARNINGS", "r", "", "m"}: 5}
	if kept, _ := matchBaseline(allowed, "CODE-ZERO-WARNINGS", []Finding{{Rule: "r", Message: "m"}}); len(kept) != 1 {
		t.Fatal("an unlocated finding was baselined")
	}
}

func TestBaselineMatchIsACountLimitedMultiset(t *testing.T) {
	fpGen := rapid.Custom(func(t *rapid.T) Fingerprint {
		return Fingerprint{"CODE-X", rapid.SampledFrom([]string{"r1", "r2"}).Draw(t, "rule"), rapid.SampledFrom([]string{"a.go", "b.go"}).Draw(t, "path"), rapid.SampledFrom([]string{"m1", "m2"}).Draw(t, "msg")}
	})
	rapid.Check(t, func(t *rapid.T) {
		allowed := map[Fingerprint]int{}
		for _, fp := range rapid.SliceOfN(fpGen, 0, 4).Draw(t, "entries") {
			allowed[fp] = rapid.IntRange(1, 3).Draw(t, "count")
		}
		var fs []Finding
		for _, fp := range rapid.SliceOfN(fpGen, 0, 10).Draw(t, "findings") {
			fs = append(fs, Finding{Located: true, Path: fp.Path, Rule: fp.Rule, Message: fp.Message})
		}
		kept, used := matchBaseline(allowed, "CODE-X", fs)
		suppressed := 0
		for fp, n := range used {
			if n > allowed[fp] {
				t.Fatalf("entry %v suppressed %d, count is %d", fp, n, allowed[fp])
			}
			suppressed += n
		}
		if suppressed+len(kept) != len(fs) {
			t.Fatalf("suppressed %d + kept %d != %d findings", suppressed, len(kept), len(fs))
		}
		for fp, n := range allowed {
			matching := 0
			for _, f := range fs {
				if (Fingerprint{"CODE-X", f.Rule, f.Path, f.Message}) == fp {
					matching++
				}
			}
			if used[fp] != min(n, matching) {
				t.Fatalf("entry %v suppressed %d, want min(count %d, matches %d)", fp, used[fp], n, matching)
			}
		}
		rev := slices.Clone(fs)
		slices.Reverse(rev)
		if kept2, used2 := matchBaseline(allowed, "CODE-X", rev); len(kept2) != len(kept) || !mapsEqual(used, used2) {
			t.Fatal("baseline match depends on finding order")
		}
	})
}

func mapsEqual(a, b map[Fingerprint]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestBaselineMarshalRoundTrips(t *testing.T) {
	b := NewBaseline([]Fingerprint{{"CODE-B", "r", "b.go", "m"}, {"CODE-A", "r", "a.go", "m"}, {"CODE-A", "r", "a.go", "m"}})
	got, err := ParseBaseline("x", b.Marshal())
	if err != nil || !slices.Equal(got.Entries, b.Entries) || b.Entries[0].Count != 2 || b.Entries[0].Objective != "CODE-A" {
		t.Fatalf("got %+v, %v from %+v", got, err, b)
	}
}
