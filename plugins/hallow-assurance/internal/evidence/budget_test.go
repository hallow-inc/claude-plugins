package evidence

import (
	"encoding/json"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestTestBudgetUnknownRoleIsLocatedAtTheRoleField(t *testing.T) {
	_, err := ParseTestBudget([]byte(`{"version":0,"files":[{"path":"a.go","role":"generated","added_cases":1}]}`))
	if err == nil {
		t.Fatal("want error for role generated")
	}
	if want := "test budget: /files/0/role: "; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("error %q does not start with %q; a bad role must be reported on the role field, not the entry", err, want)
	}
}

func TestTestBudgetReadsBackEverySchemaValidDocument(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		entry := rapid.Custom(func(t *rapid.T) BudgetFile {
			f := BudgetFile{
				Path: rapid.StringMatching(`[a-z][a-z0-9_]{0,5}(/[a-z][a-z0-9_.]{0,5}){0,3}`).Draw(t, "path"),
				Role: rapid.SampledFrom([]string{"test", "source"}).Draw(t, "role"),
			}
			n := rapid.IntRange(0, 10000).Draw(t, "n")
			if f.Role == "test" {
				f.AddedCases = n
			} else {
				f.ChangedLines = n
			}
			return f
		})
		want := rapid.SliceOfN(entry, 0, 6).Draw(t, "files")
		files := make([]map[string]any, len(want))
		for i, f := range want {
			m := map[string]any{"path": f.Path, "role": f.Role}
			if f.Role == "test" {
				m["added_cases"] = f.AddedCases
			} else {
				m["changed_lines"] = f.ChangedLines
			}
			files[i] = m
		}
		doc, err := json.Marshal(map[string]any{"version": 0, "files": files})
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseTestBudget(doc)
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		if len(got) != len(want) {
			t.Fatalf("got %d entries, want %d for %s", len(got), len(want), doc)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})
}
