package adapterkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func repoPath() *rapid.Generator[string] {
	seg := rapid.StringOfN(rapid.SampledFrom([]rune{'a', 'B', 'z', '-', '_', '.', ' ', 'é', '日'}), 1, 4, -1).
		Filter(func(s string) bool { return s != ".." })
	return rapid.Map(rapid.SliceOfN(seg, 1, 3), func(segs []string) string { return strings.Join(segs, "/") })
}

func lineRange() *rapid.Generator[[2]int] {
	return rapid.Custom(func(t *rapid.T) [2]int {
		start := rapid.IntRange(1, 60).Draw(t, "start")
		return [2]int{start, start + rapid.IntRange(0, 20).Draw(t, "len")}
	})
}

func scopeEntry() *rapid.Generator[ScopeEntry] {
	return rapid.Custom(func(t *rapid.T) ScopeEntry {
		e := ScopeEntry{Path: rapid.SampledFrom([]string{"a.go", "b/c.go"}).Draw(t, "known")}
		if rapid.Bool().Draw(t, "fresh") {
			e.Path = repoPath().Draw(t, "path")
		}
		if rapid.Bool().Draw(t, "ranged") {
			e.Ranges = rapid.SliceOfN(lineRange(), 1, 3).Draw(t, "ranges")
		}
		return e
	})
}

func scopeGen() *rapid.Generator[Scope] {
	return rapid.Map(rapid.SliceOfN(scopeEntry(), 1, 5), func(es []ScopeEntry) Scope { return Scope(es) })
}

func scopeJSON(t fataler, s Scope) []byte {
	docs := make([]map[string]any, len(s))
	for i, e := range s {
		docs[i] = map[string]any{"path": e.Path}
		if e.Ranges != nil {
			docs[i]["ranges"] = e.Ranges
		}
	}
	data, err := json.Marshal(docs)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return data
}

func writeScope(t fataler, dir string, body []byte) string {
	f, err := os.CreateTemp(dir, "scope-*.json")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := f.Write(body); err != nil {
		t.Fatalf("%v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("%v", err)
	}
	return f.Name()
}

func TestReadScopeRoundTripsValidScopes(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		want := scopeGen().Draw(t, "scope")
		file := writeScope(t, dir, scopeJSON(t, want))
		got, err := ReadScope(file)
		if err != nil {
			t.Fatalf("ReadScope rejected a valid scope %s: %v", scopeJSON(t, want), err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadScope = %#v, want %#v; the adapter must run against exactly the paths and ranges the scope file names", got, want)
		}
	})
}

func TestReadScopeRejectsInvertedRangeNamingPathAndRange(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		s := scopeGen().Draw(t, "scope")
		i := rapid.IntRange(0, len(s)-1).Draw(t, "entry")
		end := rapid.IntRange(1, 60).Draw(t, "end")
		bad := [2]int{end + rapid.IntRange(1, 20).Draw(t, "over"), end}
		ranges := slices.Clone(s[i].Ranges)
		at := rapid.IntRange(0, len(ranges)).Draw(t, "at")
		s[i].Ranges = slices.Insert(ranges, at, bad)
		file := writeScope(t, dir, scopeJSON(t, s))
		got, err := ReadScope(file)
		if err == nil {
			t.Fatalf("ReadScope accepted %s with inverted range %v and returned %#v; an inverted range is a usage error, not an empty scope", scopeJSON(t, s), bad, got)
		}
		if got != nil {
			t.Fatalf("ReadScope returned %#v alongside error %v; a caller that ignores the error must not run against a scope containing an inverted range", got, err)
		}
		for _, part := range []string{file, s[i].Path, fmt.Sprintf("[%d, %d]", bad[0], bad[1])} {
			if !strings.Contains(err.Error(), part) {
				t.Fatalf("error %q does not name %q; the user must be able to find the inverted range from the message", err, part)
			}
		}
	})
}

func TestReadScopeSurfacesSchemaViolationsWithFileName(t *testing.T) {
	dir := t.TempDir()
	invalid := []string{
		`[]`,
		`{}`,
		`[{"path": "a.go", "ranges": [[0, 3]]}]`,
		`[{"path": "a.go", "ranges": [[2, 0]]}]`,
		`[{"path": "a.go", "ranges": [[-1, 3]]}]`,
		`[{"path": "a.go", "ranges": [[1, 2, 3]]}]`,
		`[{"path": "a.go", "ranges": [[1]]}]`,
		`[{"path": "a.go", "ranges": [[1.5, 3]]}]`,
		`[{"path": "a.go", "ranges": []}]`,
		`[{"path": "a.go", "ranges": null}]`,
		`[{"ranges": [[1, 2]]}]`,
		`[{"path": "a.go", "extra": 1}]`,
		`[{"path": "/abs.go"}]`,
		`[{"path": "a/../b.go"}]`,
		`[{"path": "a\\b.go"}]`,
		`[{"path": 3}]`,
		`not json`,
	}
	rapid.Check(t, func(t *rapid.T) {
		valid := scopeJSON(t, scopeGen().Draw(t, "scope"))
		bad := rapid.SampledFrom(invalid).Draw(t, "bad")
		body := []byte(bad)
		if strings.HasPrefix(bad, "[{") && rapid.Bool().Draw(t, "embed") {
			body = []byte(string(valid[:len(valid)-1]) + "," + bad[1:])
		}
		file := writeScope(t, dir, body)
		got, err := ReadScope(file)
		if err == nil {
			t.Fatalf("ReadScope accepted %s and returned %#v; a malformed scope must stop the run rather than scope it to something unintended", body, got)
		}
		if !strings.Contains(err.Error(), file) {
			t.Fatalf("error %q does not name the scope file %s", err, file)
		}
	})
}

func TestReadScopeReportsMissingFile(t *testing.T) {
	if got, err := ReadScope(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatalf("ReadScope of a missing file returned %#v and no error", got)
	}
}

func TestScopePathsAreDistinctInFirstAppearanceOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := scopeGen().Draw(t, "scope")
		seen := map[string]bool{}
		var want []string
		for _, e := range s {
			if !seen[e.Path] {
				seen[e.Path] = true
				want = append(want, e.Path)
			}
		}
		if got := s.Paths(); !reflect.DeepEqual(got, want) {
			t.Fatalf("Paths() = %q, want %q; adapters iterate these to pick units, so a duplicate runs a unit twice and a dropped path skips it", got, want)
		}
	})
}

func TestScopeContainsMatchesLineOracle(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := scopeGen().Draw(t, "scope")
		path := rapid.SampledFrom(append(s.Paths(), "a.go", "b/c.go", "other.go")).Draw(t, "path")
		line := rapid.IntRange(0, 90).Draw(t, "line")
		whole := false
		lines := map[int]bool{}
		for _, e := range s {
			if e.Path != path {
				continue
			}
			if len(e.Ranges) == 0 {
				whole = true
			}
			for _, r := range e.Ranges {
				for l := r[0]; l <= r[1]; l++ {
					lines[l] = true
				}
			}
		}
		want := whole || lines[line]
		if got := s.Contains(path, line); got != want {
			t.Fatalf("Contains(%q, %d) = %v, want %v for %#v; mutation and coverage scoping both filter through this rule, so a wrong answer mutates or reports lines outside the scope or misses lines inside it", path, line, got, want, s)
		}
	})
}

func shuffled(t *rapid.T, groups ...[]string) []string {
	order := rapid.Permutation(groups).Draw(t, "order")
	var out []string
	for _, g := range order {
		out = append(out, g...)
	}
	return out
}

func objective() *rapid.Generator[string] {
	return rapid.SampledFrom([]string{"VER-TESTS-PASS", "VER-MUTATION-CHANGED", "CODE-LINT"})
}

func TestParseRunAcceptsChangedFromAlone(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		obj := objective().Draw(t, "objective")
		out := repoPath().Draw(t, "out")
		ref := rapid.SampledFrom([]string{"HEAD~1", "origin/master", "abc123"}).Draw(t, "ref")
		args := append([]string{obj}, shuffled(t, []string{"--out", out}, []string{"--changed-from", ref})...)
		got, err := ParseRun(args)
		if err != nil {
			t.Fatalf("ParseRun(%q): %v", args, err)
		}
		if want := (RunArgs{Objective: obj, Out: out, ChangedFrom: ref}); !reflect.DeepEqual(got, want) {
			t.Fatalf("ParseRun(%q) = %#v, want %#v", args, got, want)
		}
	})
}

func TestParseRunAcceptsScopeAloneAndLoadsIt(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		obj := objective().Draw(t, "objective")
		out := repoPath().Draw(t, "out")
		s := scopeGen().Draw(t, "scope")
		file := writeScope(t, dir, scopeJSON(t, s))
		args := append([]string{obj}, shuffled(t, []string{"--out", out}, []string{"--scope", file})...)
		got, err := ParseRun(args)
		if err != nil {
			t.Fatalf("ParseRun(%q): %v", args, err)
		}
		if want := (RunArgs{Objective: obj, Out: out, Scope: s}); !reflect.DeepEqual(got, want) {
			t.Fatalf("ParseRun(%q) = %#v, want %#v; --scope must carry the file's entries to the adapter and leave ChangedFrom empty", args, got, want)
		}
	})
}

func TestParseRunRejectsInvalidScopeFile(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		body := rapid.SampledFrom([]string{`[]`, `[{"path": "a.go", "ranges": [[5, 2]]}]`, `[{"path": "a.go", "ranges": [[0, 2]]}]`}).Draw(t, "body")
		file := writeScope(t, dir, []byte(body))
		if got, err := ParseRun([]string{"VER-TESTS-PASS", "--out", "o", "--scope", file}); err == nil {
			t.Fatalf("ParseRun accepted scope %s and returned %#v; a bad scope file is a usage error", body, got)
		}
	})
	if got, err := ParseRun([]string{"VER-TESTS-PASS", "--out", "o", "--scope", filepath.Join(dir, "absent.json")}); err == nil {
		t.Fatalf("ParseRun accepted a missing scope file and returned %#v", got)
	}
}

func TestParseRunRejectsMalformedInvocations(t *testing.T) {
	dir := t.TempDir()
	file := writeScope(t, dir, []byte(`[{"path": "a.go"}]`))
	rapid.Check(t, func(t *rapid.T) {
		out := []string{"--out", repoPath().Draw(t, "out")}
		ref := []string{"--changed-from", "HEAD~1"}
		scope := []string{"--scope", file}
		var args []string
		switch kind := rapid.SampledFrom([]string{"both", "neither", "no-out", "no-objective", "extra", "empty", "unknown-flag"}).Draw(t, "kind"); kind {
		case "both":
			args = append([]string{"VER-TESTS-PASS"}, shuffled(t, out, ref, scope)...)
		case "neither":
			args = append([]string{"VER-TESTS-PASS"}, out...)
		case "no-out":
			args = append([]string{"VER-TESTS-PASS"}, rapid.SampledFrom([][]string{ref, scope}).Draw(t, "selector")...)
		case "no-objective":
			args = nil
		case "extra":
			args = append(append([]string{"VER-TESTS-PASS"}, shuffled(t, out, rapid.SampledFrom([][]string{ref, scope}).Draw(t, "selector"))...), repoPath().Draw(t, "extra"))
		case "empty":
			args = append([]string{"VER-TESTS-PASS"}, shuffled(t, out, []string{"--changed-from", ""}, []string{"--scope", ""})...)
		case "unknown-flag":
			args = append([]string{"VER-TESTS-PASS"}, shuffled(t, out, ref, []string{"--bogus"})...)
		}
		if got, err := ParseRun(args); err == nil {
			t.Fatalf("ParseRun(%q) = %#v with no error; adapters print usage and exit 2 on any malformed invocation, so this must not reach a run", args, got)
		}
	})
}
