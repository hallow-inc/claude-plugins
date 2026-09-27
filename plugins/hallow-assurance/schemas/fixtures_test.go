package schemas

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

type expectation struct {
	Location string `json:"location"`
	Keyword  string `json:"keyword"`
	Line     int    `json:"line,omitempty"`
}

func (e expectation) String() string {
	return fmt.Sprintf("%q %s line=%d", e.Location, e.Keyword, e.Line)
}

func checkFixtures(fsys fs.FS, v validator) []string {
	var problems []string
	for k := range kinds {
		dir := string(k)
		valid, _ := fs.Glob(fsys, dir+"/valid/*")
		if len(valid) == 0 {
			problems = append(problems, dir+": no valid fixtures")
		}
		for _, f := range valid {
			data, _ := fs.ReadFile(fsys, f)
			vs, err := v.validate(k, data)
			if err != nil || len(vs) > 0 {
				problems = append(problems, fmt.Sprintf("%s: want valid, got err=%v violations=%v", f, err, vs))
			}
		}
		problems = append(problems, checkInvalid(fsys, v, k)...)
	}
	return problems
}

func checkInvalid(fsys fs.FS, v validator, k Kind) []string {
	dir := string(k) + "/invalid"
	raw, err := fs.ReadFile(fsys, dir+"/expect.json")
	if err != nil {
		return []string{dir + ": missing expect.json"}
	}
	var expect map[string][]expectation
	if err := json.Unmarshal(raw, &expect); err != nil {
		return []string{dir + "/expect.json: " + err.Error()}
	}
	var problems []string
	entries, _ := fs.ReadDir(fsys, dir)
	onDisk := map[string]bool{}
	for _, e := range entries {
		if e.Name() != "expect.json" {
			onDisk[e.Name()] = true
		}
	}
	for name := range expect {
		if !onDisk[name] {
			problems = append(problems, fmt.Sprintf("%s: expect.json lists missing fixture %s", dir, name))
		}
	}
	for name := range onDisk {
		want, ok := expect[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s/%s: not declared in expect.json", dir, name))
			continue
		}
		data, _ := fs.ReadFile(fsys, path.Join(dir, name))
		vs, err := v.validate(k, data)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s/%s: decode error %v; fixtures must fail schema validation", dir, name, err))
			continue
		}
		got := make([]string, 0, len(vs))
		for _, x := range vs {
			e := expectation{Location: x.Location, Keyword: x.Keyword}
			if k == Provenance {
				e.Line = x.Line
			}
			got = append(got, e.String())
		}
		wantS := make([]string, 0, len(want))
		for _, e := range want {
			wantS = append(wantS, e.String())
		}
		sort.Strings(got)
		sort.Strings(wantS)
		if !slices.Equal(slices.Compact(got), slices.Compact(wantS)) {
			problems = append(problems, fmt.Sprintf("%s/%s: violations %v, declared %v", dir, name, got, wantS))
		}
	}
	return problems
}

func TestFixtures(t *testing.T) {
	v, err := compiled()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range checkFixtures(os.DirFS("testdata"), v) {
		t.Error(p)
	}
}

func TestFixtureHarnessRejectsMisdeclaredFixtures(t *testing.T) {
	v, err := compiled()
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"waivers/invalid/expect.json": {Data: []byte(`{
			"wrong-reason.yaml": [{"location": "/0/expires", "keyword": "format"}],
			"ghost.yaml": [{"location": "", "keyword": "type"}]
		}`)},
		"waivers/invalid/wrong-reason.yaml": {Data: []byte("- objective: VER-X\n  scope: a\n  rationale: long enough rationale text\n  approver: TODO\n  expires: 2026-12-31\n")},
		"waivers/invalid/undeclared.yaml":   {Data: []byte("x: 1\n")},
	}
	problems := checkInvalid(fsys, v, Waivers)
	for _, want := range []string{"wrong-reason.yaml: violations", "missing fixture ghost.yaml", "undeclared.yaml: not declared"} {
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
			t.Errorf("harness did not report %q; got %v", want, problems)
		}
	}
}
