package qualification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type qcase struct {
	lang    string
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

func parseCase(lang, name string, data []byte) (qcase, error) {
	c := qcase{lang: lang, name: name, base: map[string][]byte{}, head: map[string][]byte{}}
	seen := map[string]bool{}
	var msg, expect, deps []byte
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
		case f.Name == "deps":
			deps = f.Data
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
	if err := addDepsSection(&c, deps); err != nil {
		return qcase{}, fmt.Errorf("case %s: %w", name, err)
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

var (
	depsRoot = filepath.Join("testdata", "deps")
	depsName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func addDepsSection(c *qcase, section []byte) error {
	if section == nil {
		return nil
	}
	return addDeps(c, strings.TrimSpace(string(section)))
}

func addDeps(c *qcase, set string) error {
	if !depsName.MatchString(set) {
		return fmt.Errorf("deps %q is not a dependency set name", set)
	}
	dir := filepath.Join(depsRoot, c.lang, set)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("deps %q: %w", set, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("deps %q: %s is empty", set, dir)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return fmt.Errorf("deps %q: %s is not a regular file", set, e.Name())
		}
		if _, ok := c.base[e.Name()]; ok {
			return fmt.Errorf("deps %q and base/ both supply %s", set, e.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		c.base[e.Name()] = data
	}
	return nil
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
		if !slices.Contains(caseTools[c.lang], n) || omitted[n] {
			return fmt.Errorf("omit-tools %q is not one of the %s tools %q, or is listed twice", n, c.lang, caseTools[c.lang])
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
	lang       string
	base, head map[string][]byte
	deletes    []string
	omit       []string
	deps       string
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
	lang := rapid.SampledFrom(languages()).Draw(t, "language")
	return archiveCase{
		lang:    lang,
		base:    base,
		head:    head,
		deletes: deletes,
		omit:    rapid.SliceOfDistinct(rapid.SampledFrom(caseTools[lang]), func(n string) string { return n }).Draw(t, "omit"),
		msg:     rapid.StringMatching(`((fix|feat)(\([a-z]{1,5}\))?: )?[a-z]{1,10}\n(\n[a-z]{1,10}\n)?`).Draw(t, "msg"),
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
	if a.deps != "" {
		ar.Files = append(ar.Files, txtar.File{Name: "deps", Data: []byte(a.deps + "\n")})
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
		c, err := parseCase(a.lang, "c", a.archive(mustJSON(t, a.expect)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.lang != a.lang {
			t.Fatalf("language %q, want %q", c.lang, a.lang)
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

func TestParseCaseAddsTheNamedDependencySet(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join(depsRoot, "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatalf("no dependency sets under %s", depsRoot)
	}
	rapid.Check(t, func(t *rapid.T) {
		dir := rapid.SampledFrom(dirs).Draw(t, "set")
		lang, set := filepath.Base(filepath.Dir(dir)), filepath.Base(dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string][]byte{}
		for _, e := range entries {
			if want[e.Name()], err = os.ReadFile(filepath.Join(dir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
		a := genArchiveCase(t)
		a.lang, a.omit, a.deps = lang, nil, set
		c, err := parseCase(a.lang, "c", a.archive(mustJSON(t, a.expect)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		maps.Copy(want, a.base)
		if !equalFiles(c.base, want) || !equalFiles(c.head, a.head) {
			t.Fatalf("base %q head %q, want base %q head %q: the set's files join base/ and nothing else changes", slices.Sorted(maps.Keys(c.base)), slices.Sorted(maps.Keys(c.head)), slices.Sorted(maps.Keys(want)), slices.Sorted(maps.Keys(a.head)))
		}
		clash := rapid.SampledFrom(entries).Draw(t, "clashing file").Name()
		a.base[clash] = genText(t, "clash")
		if _, err := parseCase(a.lang, "c", a.archive(mustJSON(t, a.expect))); err == nil || !strings.Contains(err.Error(), "both supply "+clash) {
			t.Fatalf("base/%s alongside set %s returned %v; one of the two would silently win", clash, set, err)
		}
		delete(a.base, clash)
		a.deps = rapid.StringMatching(`[a-z0-9-]{1,12}|[A-Z./ ]{1,4}`).Filter(func(s string) bool {
			_, err := os.Stat(filepath.Join(depsRoot, lang, s))
			return err != nil
		}).Draw(t, "unknown set")
		if _, err := parseCase(a.lang, "c", a.archive(mustJSON(t, a.expect))); err == nil || !strings.Contains(err.Error(), "deps ") {
			t.Fatalf("unknown set %q accepted (err %v)", a.deps, err)
		}
	})
}

func TestNpmCasesCommitTheirLockfileButNotNodeModules(t *testing.T) {
	for _, lang := range slices.Sorted(maps.Keys(caseSetup)) {
		if caseSetup[lang][0] != "npm" {
			continue
		}
		files, err := filepath.Glob(filepath.Join(lang, "*.txtar"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			c, err := parseCase(lang, strings.TrimSuffix(filepath.Base(f), ".txtar"), data)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"package.json", "package-lock.json"} {
				if _, ok := c.base[name]; !ok {
					t.Errorf("%s: base commit has no %s; npm ci needs both, and an uncommitted lockfile would mark every pin untracked, which no CI-gated repository produces", f, name)
				}
			}
			if !slices.Contains(nonEmptyLines(c.base[".gitignore"]), "node_modules") {
				t.Errorf("%s: base .gitignore does not ignore node_modules; the installed tree would show as changed files", f)
			}
		}
	}
}

func TestParseCaseRejectsSchemaInvalidExpectation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genArchiveCase(t)
		status := rapid.SampledFrom([]string{"fail", "advisory-fail"}).Draw(t, "status")
		bad := expectation{Objectives: append(a.expect.Objectives, expectedObjective{Objective: "QUAL-UNLISTED", Language: "go", Status: status})}
		_, err := parseCase(a.lang, "c", a.archive(mustJSON(t, bad)))
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
		_, err := parseCase(a.lang, "c", a.archive(mustJSON(t, bad)))
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
				a.omit = append(a.omit, rapid.StringMatching(`[a-z-]{1,12}`).Filter(func(n string) bool { return !slices.Contains(caseTools[a.lang], n) }).Draw(t, "unknown tool"))
				return a.archive(mustJSON(t, a.expect))
			},
			func() []byte {
				a.omit = append(a.omit, caseTools[a.lang][0], caseTools[a.lang][0])
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
		if _, err := parseCase(a.lang, "c", bad); err == nil {
			t.Fatalf("malformed archive accepted:\n%s", bad)
		}
	})
}

func TestParseCaseRejectsToolOutsideItsLanguage(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genArchiveCase(t)
		if len(a.omit) == 0 {
			t.Skip("no omitted tool")
		}
		other := rapid.StringMatching(`[a-z]{1,10}`).Filter(func(l string) bool {
			return slices.ContainsFunc(a.omit, func(n string) bool { return !slices.Contains(caseTools[l], n) })
		}).Draw(t, "other language")
		_, err := parseCase(other, "c", a.archive(mustJSON(t, a.expect)))
		if err == nil || !strings.Contains(err.Error(), "omit-tools") {
			t.Fatalf("a %s case omitting %q, read as language %s, must be rejected by the tool check, got %v", a.lang, a.omit, other, err)
		}
	})
}
