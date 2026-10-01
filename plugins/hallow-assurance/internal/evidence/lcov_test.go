package evidence

import (
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

func lcovFileGen() *rapid.Generator[LCOVFile] {
	return rapid.Custom(func(t *rapid.T) LCOVFile {
		f := LCOVFile{
			Path:  rapid.StringMatching(`[a-z][a-z0-9_]{0,5}(/[a-z][a-z0-9_.]{0,5}){0,2}`).Draw(t, "path"),
			Lines: map[int]int{},
		}
		names := rapid.SliceOfNDistinct(
			rapid.StringMatching(`[A-Za-z_][A-Za-z0-9_.]{0,8}`), 0, 4, func(s string) string { return s },
		).Draw(t, "names")
		for _, n := range names {
			start := rapid.IntRange(1, 500).Draw(t, "start")
			end := 0
			if rapid.Bool().Draw(t, "hasEnd") {
				end = start + rapid.IntRange(0, 100).Draw(t, "span")
			}
			f.Functions = append(f.Functions, LCOVFunction{
				Name: n, Start: start, End: end, Hits: rapid.IntRange(0, 1000).Draw(t, "hits"),
			})
		}
		for range rapid.IntRange(0, 6).Draw(t, "nlines") {
			f.Lines[rapid.IntRange(1, 500).Draw(t, "line")] = rapid.IntRange(0, 1000).Draw(t, "lineHits")
		}
		return f
	})
}

func TestLCOVRoundTripPreservesFilesFunctionsAndHits(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want := rapid.SliceOfN(lcovFileGen(), 0, 4).Draw(t, "files")
		got, err := ParseLCOV(FormatLCOV(want))
		if err != nil {
			t.Fatalf("parse %q: %v", FormatLCOV(want), err)
		}
		if len(got) != len(want) {
			t.Fatalf("got %d files, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i].Path != want[i].Path {
				t.Fatalf("file %d path %q, want %q", i, got[i].Path, want[i].Path)
			}
			if len(got[i].Functions) != len(want[i].Functions) ||
				(len(want[i].Functions) > 0 && !reflect.DeepEqual(got[i].Functions, want[i].Functions)) {
				t.Fatalf("file %d functions %+v, want %+v", i, got[i].Functions, want[i].Functions)
			}
			if len(got[i].Lines) != len(want[i].Lines) ||
				(len(want[i].Lines) > 0 && !reflect.DeepEqual(got[i].Lines, want[i].Lines)) {
				t.Fatalf("file %d lines %v, want %v", i, got[i].Lines, want[i].Lines)
			}
		}
	})
}

func TestLCOVScenarios(t *testing.T) {
	t.Run("both FN forms keep an absent end distinct from a present one", func(t *testing.T) {
		got, err := ParseLCOV([]byte("SF:a.go\nFN:10,Parse\nend_of_record\nSF:b.go\nFN:20,35,Decide\nend_of_record\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d files, want 2", len(got))
		}
		if want := (LCOVFunction{Name: "Parse", Start: 10}); !reflect.DeepEqual(got[0].Functions, []LCOVFunction{want}) {
			t.Fatalf("got %+v, want %+v", got[0].Functions, want)
		}
		if want := (LCOVFunction{Name: "Decide", Start: 20, End: 35}); !reflect.DeepEqual(got[1].Functions, []LCOVFunction{want}) {
			t.Fatalf("got %+v, want %+v", got[1].Functions, want)
		}
	})
	t.Run("missing end_of_record is an error so a truncated report cannot pass as complete", func(t *testing.T) {
		if _, err := ParseLCOV([]byte("SF:a.go\nFN:1,F\nFNDA:1,F\nDA:1,1\n")); err == nil {
			t.Fatal("want error for a block with no end_of_record")
		}
	})
}

func FuzzParseLCOV(f *testing.F) {
	f.Add([]byte("SF:a/b.go\nFN:1,2,F\nFNDA:3,F\nDA:1,3\nend_of_record\n"))
	f.Add([]byte("TN:\nSF:a.go\nFN:5,G\nFNDA:0,G\nDA:5,0\nend_of_record\n"))
	f.Add([]byte("SF:a.go\nDA:1,1\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		first, err := ParseLCOV(data)
		if err != nil {
			return
		}
		second, err := ParseLCOV(FormatLCOV(first))
		if err != nil {
			t.Fatalf("reparse of formatted %+v: %v", first, err)
		}
		if len(first) != len(second) {
			t.Fatalf("round trip changed file count: %+v -> %+v", first, second)
		}
		for i := range first {
			if first[i].Path != second[i].Path ||
				(len(first[i].Functions) > 0 || len(second[i].Functions) > 0) && !reflect.DeepEqual(first[i].Functions, second[i].Functions) ||
				(len(first[i].Lines) > 0 || len(second[i].Lines) > 0) && !reflect.DeepEqual(first[i].Lines, second[i].Lines) {
				t.Fatalf("round trip changed file %d: %+v -> %+v", i, first[i], second[i])
			}
		}
	})
}
