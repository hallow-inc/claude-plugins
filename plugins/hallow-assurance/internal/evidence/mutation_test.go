package evidence

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var strykerStatuses = []string{"Killed", "Survived", "NoCoverage", "CompileError", "RuntimeError", "Timeout", "Ignored", "Pending"}

func strykerDoc(files map[string][]Mutant) []byte {
	out := map[string]any{}
	for p, ms := range files {
		mutants := []any{}
		for i, m := range ms {
			pos := map[string]any{"line": m.Line, "column": 1}
			mutants = append(mutants, map[string]any{
				"id":          fmt.Sprintf("%s:%d:%d", p, m.Line, i),
				"mutatorName": m.Mutator,
				"location":    map[string]any{"start": pos, "end": pos},
				"status":      m.Status,
			})
		}
		out[p] = map[string]any{"language": "go", "source": "package x\n", "mutants": mutants}
	}
	data, _ := json.Marshal(map[string]any{"schemaVersion": "1", "thresholds": map[string]any{"high": 80, "low": 60}, "files": out})
	return data
}

func TestMutationReportRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		files := map[string][]Mutant{}
		for range rapid.IntRange(0, 3).Draw(t, "nfiles") {
			p := rapid.StringMatching(`[a-z]{1,6}(/[a-z]{1,6}){0,2}\.go`).Draw(t, "path")
			var ms []Mutant
			for range rapid.IntRange(0, 4).Draw(t, "nmutants") {
				ms = append(ms, Mutant{
					Path:    p,
					Line:    rapid.IntRange(1, 500).Draw(t, "line"),
					Mutator: rapid.StringMatching(`[A-Z]{1,6}(_[A-Z]{1,6})?`).Draw(t, "mutator"),
					Status:  rapid.SampledFrom(strykerStatuses).Draw(t, "status"),
				})
			}
			files[p] = ms
		}
		got, err := ParseMutationReport(strykerDoc(files))
		if err != nil {
			t.Fatal(err)
		}
		want := []Mutant{}
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			want = append(want, files[p]...)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}

func TestMutationReportValidReportIsRead(t *testing.T) {
	doc := strykerDoc(map[string][]Mutant{"internal/app/check.go": {{Line: 42, Mutator: "CONDITIONALS_BOUNDARY", Status: "Survived"}}})
	got, err := ParseMutationReport(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []Mutant{{Path: "internal/app/check.go", Line: 42, Mutator: "CONDITIONALS_BOUNDARY", Status: "Survived"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMutationReportRejects(t *testing.T) {
	for name, doc := range map[string][]byte{
		"gremlins status Lived": strykerDoc(map[string][]Mutant{"a.go": {{Line: 1, Mutator: "M", Status: "Lived"}}}),
		"traversal path":        strykerDoc(map[string][]Mutant{"../outside.go": {{Line: 1, Mutator: "M", Status: "Killed"}}}),
		"nested traversal":      strykerDoc(map[string][]Mutant{"a/../../b.go": {}}),
		"absolute path":         strykerDoc(map[string][]Mutant{"/etc/a.go": {}}),
		"backslash path":        strykerDoc(map[string][]Mutant{`a\b.go`: {}}),
		"empty path":            strykerDoc(map[string][]Mutant{"": {}}),
		"not json":              []byte(`{"schemaVersion":`),
		"missing files":         []byte(`{"schemaVersion":"1","thresholds":{"high":80,"low":60}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseMutationReport(doc); err == nil {
				t.Fatal("accepted; a mutant's path decides its level, so a bad report must not be scored")
			}
		})
	}
}

func TestMutationReportAcceptsDottedNames(t *testing.T) {
	doc := strykerDoc(map[string][]Mutant{"a/..b/c..go": {{Line: 1, Mutator: "M", Status: "Killed"}}})
	if _, err := ParseMutationReport(doc); err != nil {
		t.Fatalf("%v; only a whole .. segment is traversal", err)
	}
	if !strings.Contains(string(doc), "..b") {
		t.Fatal("fixture lost its dotted segment")
	}
}
