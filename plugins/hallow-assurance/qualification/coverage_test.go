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

func loadCases(t *testing.T, lang string) map[string]loadedCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(lang, "*.txtar"))
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
		c, err := parseCase(lang, name, data)
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

func statusOf(e expectation, lang, objective string) string {
	for _, o := range e.Objectives {
		if o.Objective == objective && o.Language == lang {
			return o.Status
		}
	}
	return ""
}

func coverageGaps(lang string, required []string, cases map[string]loadedCase) []string {
	var gaps []string
	for _, id := range required {
		seeded := false
		for _, c := range cases {
			seeded = seeded || c.seeds == id && statusOf(c.expect, lang, id) == "fail"
		}
		if !seeded {
			gaps = append(gaps, id+": no seeded case expects it to fail")
		}
		if clean, ok := cases["clean"]; !ok || statusOf(clean.expect, lang, id) != "pass" {
			gaps = append(gaps, id+": clean does not expect it to pass")
		}
	}
	return gaps
}

func adapterObjectives(t *testing.T, lang string) map[string]json.RawMessage {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), filepath.Join(binDir, adapterName(lang)), "describe").Output()
	if err != nil {
		t.Fatalf("%s describe: %v", adapterName(lang), err)
	}
	var d struct {
		Objectives map[string]json.RawMessage `json:"objectives"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	return d.Objectives
}

func requiredObjectives(t *testing.T, lang string) []string {
	t.Helper()
	implemented := adapterObjectives(t, lang)
	cat, err := core.LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	var req []string
	for _, o := range cat.Objectives {
		if _, ok := implemented[o.ID]; ok && o.Levels["B"] == "required" {
			req = append(req, o.ID)
		}
	}
	slices.Sort(req)
	return req
}

func TestEveryRequiredObjectiveIsQualified(t *testing.T) {
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to check case coverage against the built adapter")
	}
	for _, lang := range languages() {
		t.Run(lang, func(t *testing.T) {
			required := requiredObjectives(t, lang)
			if len(required) == 0 {
				t.Fatalf("the %s adapter implements no objective the catalog requires at level B", lang)
			}
			cases := loadCases(t, lang)
			if gaps := coverageGaps(lang, required, cases); len(gaps) > 0 {
				t.Fatalf("qualification does not cover every required %s objective:\n%s", lang, strings.Join(gaps, "\n"))
			}
			for _, name := range slices.Sorted(maps.Keys(cases)) {
				rest := maps.Clone(cases)
				delete(rest, name)
				gaps := coverageGaps(lang, required, rest)
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
					t.Errorf("%s with %s moved aside: gaps %q, want %q", lang, name, gaps, want)
				}
			}
		})
	}
}
