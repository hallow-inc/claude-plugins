package qualification

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var seedsPrefix = regexp.MustCompile(`^([A-Z]+(?:-[A-Z]+)+):`)

type loadedCase struct {
	seeds  string
	expect expectation
}

func loadCases(t *testing.T) map[string]loadedCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("go", "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]loadedCase{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".txtar")
		c, err := parseCase(name, data)
		if err != nil {
			t.Fatal(err)
		}
		lc := loadedCase{expect: c.expect}
		if m := seedsPrefix.FindSubmatch(txtar.Parse(data).Comment); m != nil {
			lc.seeds = string(m[1])
		}
		out[name] = lc
	}
	return out
}

func statusOf(e expectation, objective string) string {
	for _, o := range e.Objectives {
		if o.Objective == objective && o.Language == "go" {
			return o.Status
		}
	}
	return ""
}

func coverageGaps(required []string, cases map[string]loadedCase) []string {
	var gaps []string
	for _, id := range required {
		seeded := false
		for _, c := range cases {
			seeded = seeded || c.seeds == id && statusOf(c.expect, id) == "fail"
		}
		if !seeded {
			gaps = append(gaps, id+": no seeded case expects it to fail")
		}
		if clean, ok := cases["clean"]; !ok || statusOf(clean.expect, id) != "pass" {
			gaps = append(gaps, id+": clean does not expect it to pass")
		}
	}
	return gaps
}

func requiredGoObjectives(t *testing.T) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), filepath.Join(binDir, "assure-adapter-go"), "describe").Output()
	if err != nil {
		t.Fatalf("assure-adapter-go describe: %v", err)
	}
	var d struct {
		Objectives map[string]json.RawMessage `json:"objectives"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	cat, err := core.LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	var req []string
	for _, o := range cat.Objectives {
		if _, ok := d.Objectives[o.ID]; ok && o.Levels["B"] == "required" {
			req = append(req, o.ID)
		}
	}
	slices.Sort(req)
	return req
}

func TestEveryRequiredGoObjectiveIsQualified(t *testing.T) {
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to check case coverage against the built adapter")
	}
	required := requiredGoObjectives(t)
	if len(required) == 0 {
		t.Fatal("the adapter implements no objective the catalog requires at level B")
	}
	cases := loadCases(t)
	if gaps := coverageGaps(required, cases); len(gaps) > 0 {
		t.Fatalf("qualification does not cover every required Go objective:\n%s", strings.Join(gaps, "\n"))
	}
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		rest := maps.Clone(cases)
		delete(rest, name)
		gaps := coverageGaps(required, rest)
		var want []string
		for _, id := range required {
			if name == "clean" {
				want = append(want, id+": clean does not expect it to pass")
			}
			if cases[name].seeds == id && !slices.ContainsFunc(slices.Collect(maps.Values(rest)), func(c loadedCase) bool { return c.seeds == id }) {
				want = append(want, id+": no seeded case expects it to fail")
			}
		}
		if !slices.Equal(gaps, want) {
			t.Errorf("with %s moved aside: gaps %q, want %q", name, gaps, want)
		}
	}
}
