package qualification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type qcase struct {
	name    string
	base    map[string][]byte
	head    map[string][]byte
	deletes []string
	omit    []string
	msg     string
	expect  expectation
}

type expectation struct {
	Objectives []expectedObjective `json:"objectives"`
	Problems   []string            `json:"problems,omitempty"`
}

type expectedObjective struct {
	Objective string   `json:"objective"`
	Language  string   `json:"language,omitempty"`
	Status    string   `json:"status"`
	Locations []string `json:"locations,omitempty"`
}

func parseCase(name string, data []byte) (qcase, error) {
	c := qcase{name: name, base: map[string][]byte{}, head: map[string][]byte{}}
	seen := map[string]bool{}
	var msg, expect []byte
	for _, f := range txtar.Parse(data).Files {
		if seen[f.Name] {
			return qcase{}, fmt.Errorf("case %s: duplicate section %q", name, f.Name)
		}
		seen[f.Name] = true
		switch {
		case f.Name == "head.msg":
			msg = f.Data
		case f.Name == "expect.json":
			expect = f.Data
		case f.Name == "delete":
			c.deletes = nonEmptyLines(f.Data)
		case f.Name == "omit-tools":
			c.omit = nonEmptyLines(f.Data)
		case strings.HasPrefix(f.Name, "base/"):
			c.base[strings.TrimPrefix(f.Name, "base/")] = f.Data
		case strings.HasPrefix(f.Name, "head/"):
			c.head[strings.TrimPrefix(f.Name, "head/")] = f.Data
		default:
			return qcase{}, fmt.Errorf("case %s: unknown section %q", name, f.Name)
		}
	}
	if msg == nil {
		return qcase{}, fmt.Errorf("case %s: missing head.msg section", name)
	}
	if expect == nil {
		return qcase{}, fmt.Errorf("case %s: missing expect.json section", name)
	}
	if err := checkPaths(c); err != nil {
		return qcase{}, fmt.Errorf("case %s: %w", name, err)
	}
	c.msg = string(msg)
	exp, err := parseExpectation(expect)
	if err != nil {
		return qcase{}, fmt.Errorf("case %s: expect.json: %w", name, err)
	}
	c.expect = exp
	return c, nil
}

func nonEmptyLines(data []byte) []string {
	var out []string
	for line := range strings.Lines(string(data)) {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func checkPaths(c qcase) error {
	if len(c.base) == 0 {
		return fmt.Errorf("no base/ files")
	}
	for _, p := range slices.Concat(slices.Collect(maps.Keys(c.base)), slices.Collect(maps.Keys(c.head)), c.deletes) {
		if !localPath(p) {
			return fmt.Errorf("path %q is not a clean relative path", p)
		}
	}
	gone := map[string]bool{}
	for _, p := range c.deletes {
		if _, ok := c.base[p]; !ok || gone[p] {
			return fmt.Errorf("delete %q names no base file, or names it twice", p)
		}
		if _, ok := c.head[p]; ok {
			return fmt.Errorf("%q is both deleted and in head/", p)
		}
		gone[p] = true
	}
	omitted := map[string]bool{}
	for _, n := range c.omit {
		if !slices.Contains(caseTools, n) || omitted[n] {
			return fmt.Errorf("omit-tools %q is not one of %q, or is listed twice", n, caseTools)
		}
		omitted[n] = true
	}
	return nil
}

func localPath(p string) bool {
	return p != "" && path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../")
}

func parseExpectation(data []byte) (expectation, error) {
	vs, err := schemas.Validate(schemas.QualExpect, data)
	if err != nil {
		return expectation{}, err
	}
	if len(vs) > 0 {
		return expectation{}, fmt.Errorf("schema violations: %v", vs)
	}
	var e expectation
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return expectation{}, err
	}
	keys := map[entryKey]bool{}
	for _, o := range e.Objectives {
		k := entryKey{o.Objective, o.Language}
		if keys[k] {
			return expectation{}, fmt.Errorf("%s listed twice", k)
		}
		keys[k] = true
	}
	return e, nil
}

func genText(t *rapid.T, label string) []byte {
	s := rapid.StringMatching(`([a-z ]{0,12}\n){0,3}`).Draw(t, label)
	return []byte(s)
}

func genRelPath(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-z]{1,4}(/[a-z]{1,4}){0,2}\.(go|txt)`).Draw(t, label)
}

type archiveCase struct {
	base, head map[string][]byte
	deletes    []string
	omit       []string
	msg        string
	expect     expectation
}

func genArchiveCase(t *rapid.T) archiveCase {
	base := rapid.MapOfN(rapid.Custom(func(t *rapid.T) string { return genRelPath(t, "p") }), rapid.Custom(func(t *rapid.T) []byte { return genText(t, "b") }), 1, 4).Draw(t, "base")
	paths := slices.Sorted(maps.Keys(base))
	var deletes []string
	head := map[string][]byte{}
	for _, p := range paths {
		switch rapid.IntRange(0, 2).Draw(t, "fate "+p) {
		case 1:
			head[p] = genText(t, "head "+p)
		case 2:
			deletes = append(deletes, p)
		}
	}
	for _, p := range rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string { return genRelPath(t, "np") }), 0, 2).Draw(t, "new") {
		if _, ok := base[p]; !ok {
			head[p] = genText(t, "new "+p)
		}
	}
	return archiveCase{
		base:    base,
		head:    head,
		deletes: deletes,
		omit:    rapid.SliceOfDistinct(rapid.SampledFrom(caseTools), func(n string) string { return n }).Draw(t, "omit"),
		msg:     rapid.StringMatching(`[a-z]{1,10}\n(\nAssure-Kind: fix\n)?`).Draw(t, "msg"),
		expect:  genExpectation(t),
	}
}

func (a archiveCase) archive(expectJSON []byte) []byte {
	ar := &txtar.Archive{Comment: []byte("case description\n")}
	for _, p := range slices.Sorted(maps.Keys(a.base)) {
		ar.Files = append(ar.Files, txtar.File{Name: "base/" + p, Data: a.base[p]})
	}
	for _, p := range slices.Sorted(maps.Keys(a.head)) {
		ar.Files = append(ar.Files, txtar.File{Name: "head/" + p, Data: a.head[p]})
	}
	if len(a.deletes) > 0 {
		ar.Files = append(ar.Files, txtar.File{Name: "delete", Data: []byte(strings.Join(a.deletes, "\n") + "\n")})
	}
	if len(a.omit) > 0 {
		ar.Files = append(ar.Files, txtar.File{Name: "omit-tools", Data: []byte(strings.Join(a.omit, "\n") + "\n")})
	}
	ar.Files = append(ar.Files,
		txtar.File{Name: "head.msg", Data: []byte(a.msg)},
		txtar.File{Name: "expect.json", Data: expectJSON},
	)
	return txtar.Format(ar)
}

func mustJSON(t interface{ Fatalf(string, ...any) }, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func equalFiles(a, b map[string][]byte) bool {
	return maps.EqualFunc(a, b, bytes.Equal)
}

func TestParseCaseRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genArchiveCase(t)
		c, err := parseCase("c", a.archive(mustJSON(t, a.expect)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !equalFiles(c.base, a.base) || !equalFiles(c.head, a.head) {
			t.Fatalf("files: got base=%q head=%q, want base=%q head=%q", c.base, c.head, a.base, a.head)
		}
		if !slices.Equal(c.deletes, a.deletes) || !slices.Equal(c.omit, a.omit) || c.msg != a.msg {
			t.Fatalf("got deletes=%q omit=%q msg=%q, want deletes=%q omit=%q msg=%q", c.deletes, c.omit, c.msg, a.deletes, a.omit, a.msg)
		}
		if got, want := mustJSON(t, c.expect), mustJSON(t, a.expect); !bytes.Equal(got, want) {
			t.Fatalf("expectation: got %s, want %s", got, want)
		}
	})
}

func TestParseCaseRejectsSchemaInvalidExpectation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genArchiveCase(t)
		status := rapid.SampledFrom([]string{"fail", "advisory-fail"}).Draw(t, "status")
		bad := expectation{Objectives: append(a.expect.Objectives, expectedObjective{Objective: "QUAL-UNLISTED", Language: "go", Status: status})}
		_, err := parseCase("c", a.archive(mustJSON(t, bad)))
		if err == nil || !strings.Contains(err.Error(), "schema violations") {
			t.Fatalf("a %s entry without locations must fail schema validation, got %v", status, err)
		}
	})
}

func TestParseCaseRejectsDuplicateExpectationEntry(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genArchiveCase(t)
		if len(a.expect.Objectives) == 0 {
			t.Skip("no entry to duplicate")
		}
		dup := rapid.SampledFrom(a.expect.Objectives).Draw(t, "duplicated")
		dup.Status = rapid.SampledFrom(statuses).Draw(t, "duplicate status")
		dup.Locations = rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string { return genLocation(t, "loc") }), 1, 2).Draw(t, "duplicate locations")
		bad := expectation{Objectives: append(slices.Clone(a.expect.Objectives), dup), Problems: a.expect.Problems}
		k := entryKey{dup.Objective, dup.Language}
		_, err := parseCase("c", a.archive(mustJSON(t, bad)))
		if err == nil || !strings.Contains(err.Error(), k.String()+" listed twice") {
			t.Fatalf("expectation listing %s twice must be rejected by the duplicate check, got %v", k, err)
		}
	})
}

func TestParseCaseRejectsMalformedArchive(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genArchiveCase(t)
		good := a.archive(mustJSON(t, a.expect))
		bad := rapid.SampledFrom([]func() []byte{
			func() []byte { return append(slices.Clone(good), "-- other/x --\n"...) },
			func() []byte { return append(slices.Clone(good), "-- head.msg --\nagain\n"...) },
			func() []byte { return append(slices.Clone(good), "-- base/../escape.go --\n"...) },
			func() []byte { return append(slices.Clone(good), "-- base//abs.go --\n"...) },
			func() []byte {
				a.deletes = append(a.deletes, "never/in/base.txt")
				return a.archive(mustJSON(t, a.expect))
			},
			func() []byte {
				a.omit = append(a.omit, rapid.StringMatching(`[a-z-]{1,12}`).Filter(func(n string) bool { return !slices.Contains(caseTools, n) }).Draw(t, "unknown tool"))
				return a.archive(mustJSON(t, a.expect))
			},
			func() []byte {
				a.omit = append(a.omit, caseTools[0], caseTools[0])
				return a.archive(mustJSON(t, a.expect))
			},
			func() []byte {
				ar := txtar.Parse(good)
				ar.Files = slices.DeleteFunc(ar.Files, func(f txtar.File) bool { return f.Name == "expect.json" })
				return txtar.Format(ar)
			},
			func() []byte {
				ar := txtar.Parse(good)
				ar.Files = slices.DeleteFunc(ar.Files, func(f txtar.File) bool { return f.Name == "head.msg" })
				return txtar.Format(ar)
			},
			func() []byte {
				ar := txtar.Parse(good)
				ar.Files = slices.DeleteFunc(ar.Files, func(f txtar.File) bool { return strings.HasPrefix(f.Name, "base/") })
				return txtar.Format(ar)
			},
		}).Draw(t, "malformation")()
		if _, err := parseCase("c", bad); err == nil {
			t.Fatalf("malformed archive accepted:\n%s", bad)
		}
	})
}
