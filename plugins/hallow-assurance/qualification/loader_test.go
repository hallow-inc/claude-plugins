package qualification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type qcase struct {
	lang      string
	name      string
	base      map[string][]byte
	head      map[string][]byte
	deletes   []string
	omit      []string
	msg       string
	expect    expectation
	scope     adapterkit.Scope
	survivors []string
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
	var msg, deps []byte
	outcome := map[string][]byte{}
	for _, f := range txtar.Parse(data).Files {
		if seen[f.Name] {
			return qcase{}, fmt.Errorf("case %s: duplicate section %q", name, f.Name)
		}
		seen[f.Name] = true
		switch {
		case f.Name == "head.msg":
			msg = f.Data
		case outcomeSections[f.Name]:
			outcome[f.Name] = f.Data
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
	if err := checkOutcomeKind(outcome); err != nil {
		return qcase{}, fmt.Errorf("case %s: %w", name, err)
	}
	if err := addDepsSection(&c, deps); err != nil {
		return qcase{}, fmt.Errorf("case %s: %w", name, err)
	}
	if err := checkPaths(c); err != nil {
		return qcase{}, fmt.Errorf("case %s: %w", name, err)
	}
	c.msg = string(msg)
	if err := addOutcome(&c, outcome); err != nil {
		return qcase{}, fmt.Errorf("case %s: %w", name, err)
	}
	return c, nil
}

var outcomeSections = map[string]bool{"expect.json": true, "scope.json": true, "survivors": true}

func checkOutcomeKind(outcome map[string][]byte) error {
	_, expect := outcome["expect.json"]
	_, scope := outcome["scope.json"]
	_, survivors := outcome["survivors"]
	switch {
	case expect && (scope || survivors):
		return fmt.Errorf("expect.json mixes with scope.json or survivors; a case either evaluates or runs a scope")
	case scope != survivors:
		return fmt.Errorf("scope.json and survivors come as a pair")
	case !expect && !scope:
		return fmt.Errorf("missing expect.json section")
	}
	return nil
}

func addOutcome(c *qcase, outcome map[string][]byte) error {
	if data, ok := outcome["scope.json"]; ok {
		s, err := parseScope(data)
		if err != nil {
			return fmt.Errorf("scope.json: %w", err)
		}
		c.scope = s
		if c.survivors, err = parseSurvivors(outcome["survivors"]); err != nil {
			return fmt.Errorf("survivors: %w", err)
		}
		return nil
	}
	exp, err := parseExpectation(outcome["expect.json"])
	if err != nil {
		return fmt.Errorf("expect.json: %w", err)
	}
	c.expect = exp
	return nil
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

func parseScope(data []byte) (adapterkit.Scope, error) {
	vs, err := schemas.Validate(schemas.AdapterScope, data)
	if err != nil {
		return nil, err
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("schema violations: %v", vs)
	}
	var s adapterkit.Scope
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return s, nil
}

var survivorLine = regexp.MustCompile(`^(.+):([1-9][0-9]*)$`)

func parseSurvivors(data []byte) ([]string, error) {
	lines := nonEmptyLines(data)
	if len(lines) == 0 {
		return nil, fmt.Errorf("no path:line entries; an empty set passes for an adapter that emits no evidence at all")
	}
	seen := map[string]bool{}
	for _, l := range lines {
		m := survivorLine.FindStringSubmatch(l)
		if m == nil || !localPath(m[1]) {
			return nil, fmt.Errorf("%q is not path:line with a clean relative path and a line from 1", l)
		}
		if seen[l] {
			return nil, fmt.Errorf("%q listed twice", l)
		}
		seen[l] = true
	}
	return lines, nil
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
	scope      adapterkit.Scope
	survivors  []string
}

func genScopeArchiveCase(t *rapid.T) archiveCase {
	a := genArchiveCase(t)
	a.expect = expectation{}
	a.scope = genScope(t)
	a.survivors = rapid.SliceOfNDistinct(rapid.Custom(func(t *rapid.T) string {
		return fmt.Sprintf("%s:%d", genRelPath(t, "survivor path"), rapid.IntRange(1, 9999).Draw(t, "survivor line"))
	}), 1, 4, rapid.ID[string]).Draw(t, "survivors")
	return a
}

func genScope(t *rapid.T) adapterkit.Scope {
	n := rapid.IntRange(1, 3).Draw(t, "scope entries")
	s := make(adapterkit.Scope, n)
	for i := range s {
		s[i].Path = genRelPath(t, "scope path")
		if rapid.Bool().Draw(t, "ranged") {
			s[i].Ranges = make([][2]int, rapid.IntRange(1, 3).Draw(t, "ranges"))
			for j := range s[i].Ranges {
				start := rapid.IntRange(1, 5000).Draw(t, "start")
				s[i].Ranges[j] = [2]int{start, start + rapid.IntRange(0, 200).Draw(t, "span")}
			}
		}
	}
	return s
}

func scopeJSON(s adapterkit.Scope) []byte {
	entries := make([]map[string]any, len(s))
	for i, e := range s {
		entries[i] = map[string]any{"path": e.Path}
		if e.Ranges != nil {
			entries[i]["ranges"] = e.Ranges
		}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		panic(err)
	}
	return b
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
	ar.Files = append(ar.Files, txtar.File{Name: "head.msg", Data: []byte(a.msg)})
	if a.scope == nil {
		ar.Files = append(ar.Files, txtar.File{Name: "expect.json", Data: expectJSON})
		return txtar.Format(ar)
	}
	ar.Files = append(ar.Files,
		txtar.File{Name: "scope.json", Data: scopeJSON(a.scope)},
		txtar.File{Name: "survivors", Data: []byte(strings.Join(a.survivors, "\n") + "\n")},
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
		a := rapid.SampledFrom([]func(*rapid.T) archiveCase{genArchiveCase, genScopeArchiveCase}).Draw(t, "kind")(t)
		c, err := parseCase(a.lang, "c", a.archive(mustJSON(t, a.expect)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !reflect.DeepEqual(c.scope, a.scope) || !slices.Equal(c.survivors, a.survivors) {
			t.Fatalf("got scope=%+v survivors=%q, want scope=%+v survivors=%q", c.scope, c.survivors, a.scope, a.survivors)
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

func TestUvCasesCommitTheirLockfileAndInterpreterButNotTheVirtualenv(t *testing.T) {
	for _, lang := range slices.Sorted(maps.Keys(caseSetup)) {
		if caseSetup[lang][0] != "uv" {
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
			for _, name := range []string{"pyproject.toml", "uv.lock"} {
				if _, ok := c.base[name]; !ok {
					t.Errorf("%s: base commit has no %s; uv sync --locked needs both, and an uncommitted lockfile would mark every pin untracked", f, name)
				}
			}
			if v := strings.TrimSpace(string(c.base[".python-version"])); !regexp.MustCompile(`^3\.[0-9]+\.[0-9]+$`).MatchString(v) {
				t.Errorf("%s: base .python-version is %q, want an exact CPython version; without it uv may pick a free-threaded build the tools do not support", f, v)
			}
			if !slices.Contains(nonEmptyLines(c.base[".gitignore"]), ".venv/") {
				t.Errorf("%s: base .gitignore does not ignore .venv/; the installed tree would show as changed files", f)
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

func TestParseCaseRejectsMalformedScopeCase(t *testing.T) {
	badScopes := []string{
		`[]`, `{}`, `[{}]`, `[{"path":"a.go","extra":1}]`, `[{"path":"/a.go"}]`, `[{"path":"a.go","ranges":[]}]`,
		`[{"path":"a.go","ranges":[[0,3]]}]`, `[{"path":"a.go","ranges":[[3]]}]`, `[{"path":"a.go","ranges":[[1,2,3]]}]`, `[{"path":"a.go","ranges":[[1.5,2]]}]`,
	}
	rapid.Check(t, func(t *rapid.T) {
		a := genScopeArchiveCase(t)
		ar := txtar.Parse(a.archive(nil))
		set := func(name string, data []byte) {
			for i := range ar.Files {
				if ar.Files[i].Name == name {
					ar.Files[i].Data = data
				}
			}
		}
		drop := func(name string) {
			ar.Files = slices.DeleteFunc(ar.Files, func(f txtar.File) bool { return f.Name == name })
		}
		var want string
		switch rapid.IntRange(0, 4).Draw(t, "malformation") {
		case 0:
			ar.Files = append(ar.Files, txtar.File{Name: "expect.json", Data: mustJSON(t, genExpectation(t))})
			if rapid.Bool().Draw(t, "drop a half") {
				drop(rapid.SampledFrom([]string{"scope.json", "survivors"}).Draw(t, "dropped"))
			}
			want = "mixes"
		case 1:
			drop(rapid.SampledFrom([]string{"scope.json", "survivors"}).Draw(t, "dropped"))
			want = "come as a pair"
		case 2:
			set("scope.json", []byte(rapid.SampledFrom(badScopes).Draw(t, "bad scope")))
			want = "scope.json: schema violations"
		case 3:
			bad := rapid.OneOf(
				rapid.StringMatching(`[a-z]{1,4}\.go(:(0|-[0-9]{1,3}|0[0-9]{1,3}|[a-z]{1,3}|[0-9]{1,3}[a-z]))?`),
				rapid.StringMatching(`(\.\./|/|[a-z]{1,3}//|)[a-z]{1,4}\.go:[1-9][0-9]{0,3}`).Filter(func(s string) bool { return !localPath(s[:strings.LastIndex(s, ":")]) }),
				rapid.StringMatching(`:[1-9][0-9]{0,3}`),
			).Draw(t, "bad survivor")
			lines := append(slices.Clone(a.survivors), bad)
			set("survivors", []byte(strings.Join(rapid.Permutation(lines).Draw(t, "order"), "\n")+"\n"))
			want = "survivors: " + strconv.Quote(bad) + " is not path:line"
		case 4:
			dup := rapid.SampledFrom(a.survivors).Draw(t, "duplicate")
			set("survivors", []byte(strings.Join(append(slices.Clone(a.survivors), dup), "\n")+"\n"))
			want = "survivors: " + strconv.Quote(dup) + " listed twice"
		}
		data := txtar.Format(ar)
		if _, err := parseCase(a.lang, "c", data); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v, want an error containing %q:\n%s", err, want, data)
		}
	})
}

func TestParseCaseRejectsEmptySurvivors(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genScopeArchiveCase(t)
		ar := txtar.Parse(a.archive(nil))
		for i := range ar.Files {
			if ar.Files[i].Name == "survivors" {
				ar.Files[i].Data = []byte(rapid.StringMatching(`[ \t\n]{0,6}`).Draw(t, "blank"))
			}
		}
		if _, err := parseCase(a.lang, "c", txtar.Format(ar)); err == nil || !strings.Contains(err.Error(), "no path:line entries") {
			t.Fatalf("blank survivors accepted (err %v); an empty expected set passes for an adapter that drops all mutation evidence", err)
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
