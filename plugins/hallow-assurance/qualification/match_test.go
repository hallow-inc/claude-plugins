package qualification

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type entryKey struct {
	objective, language string
}

func (k entryKey) String() string {
	if k.language == "" {
		return k.objective
	}
	return k.objective + " (" + k.language + ")"
}

type report struct {
	Problems   []string          `json:"problems"`
	Objectives []reportObjective `json:"objectives"`
}

type reportObjective struct {
	Objective string   `json:"objective"`
	Language  string   `json:"language,omitempty"`
	Status    string   `json:"status"`
	Details   []string `json:"details"`
}

func parseReport(data []byte) (report, error) {
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return report{}, fmt.Errorf("report: %w", err)
	}
	return r, nil
}

func match(exp expectation, rep report) []string {
	var out []string
	got := map[entryKey]reportObjective{}
	for _, o := range rep.Objectives {
		k := entryKey{o.Objective, o.Language}
		if _, dup := got[k]; dup {
			out = append(out, fmt.Sprintf("%s: expected one report entry, actual more than one", k))
		}
		got[k] = o
	}
	want := map[entryKey]bool{}
	for _, e := range exp.Objectives {
		k := entryKey{e.Objective, e.Language}
		want[k] = true
		o, ok := got[k]
		if !ok {
			out = append(out, fmt.Sprintf("%s: expected status %q, actual no report entry", k, e.Status))
			continue
		}
		if o.Status != e.Status {
			out = append(out, fmt.Sprintf("%s: expected status %q, actual %q", k, e.Status, o.Status))
		}
		lines := detailLines(o.Details)
		for _, loc := range e.Locations {
			if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, loc) }) {
				out = append(out, fmt.Sprintf("%s: expected a detail line starting with %q, actual detail lines %q", k, loc, lines))
			}
		}
	}
	for _, o := range rep.Objectives {
		if k := (entryKey{o.Objective, o.Language}); !want[k] {
			out = append(out, fmt.Sprintf("%s: expected no report entry, actual status %q", k, o.Status))
		}
	}
	if !slices.Equal(exp.Problems, rep.Problems) {
		out = append(out, fmt.Sprintf("problems: expected %q, actual %q", exp.Problems, rep.Problems))
	}
	return out
}

func detailLines(details []string) []string {
	var lines []string
	for _, d := range details {
		lines = append(lines, strings.Split(d, "\n")...)
	}
	return lines
}

var statuses = []string{"pass", "fail", "advisory-fail", "waived"}

func needsLocation(status string) bool {
	return status == "fail" || status == "advisory-fail"
}

func genKey(t *rapid.T, label string) entryKey {
	return entryKey{
		objective: rapid.StringMatching(`(CODE|VER|CFG)-[A-Z]{1,6}`).Draw(t, label+" objective"),
		language:  rapid.SampledFrom([]string{"", "go", "ts"}).Draw(t, label+" language"),
	}
}

func genLocation(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-z]{1,4}(/[a-z]{1,4})?\.go(:[0-9]{1,3})?|(FAIL|PASS): Test[A-Z][a-z]{0,6}`).Draw(t, label)
}

func genExpectation(t *rapid.T) expectation {
	keys := rapid.SliceOfNDistinct(rapid.Custom(func(t *rapid.T) entryKey { return genKey(t, "key") }), 0, 5, func(k entryKey) entryKey { return k }).Draw(t, "keys")
	e := expectation{Objectives: []expectedObjective{}}
	for i, k := range keys {
		status := rapid.SampledFrom(statuses).Draw(t, fmt.Sprintf("status %d", i))
		minLocs := 0
		if needsLocation(status) {
			minLocs = 1
		}
		locs := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) string { return genLocation(t, "loc") }), minLocs, 3).Draw(t, fmt.Sprintf("locations %d", i))
		e.Objectives = append(e.Objectives, expectedObjective{Objective: k.objective, Language: k.language, Status: status, Locations: locs})
	}
	e.Problems = rapid.SliceOfN(rapid.StringMatching(`[a-z][a-z ]{0,15}`), 0, 3).Draw(t, "problems")
	if len(e.Problems) == 0 {
		e.Problems = nil
	}
	return e
}

func reportFrom(t *rapid.T, e expectation) report {
	r := report{Problems: slices.Clone(e.Problems)}
	for i, o := range e.Objectives {
		var lines []string
		for _, loc := range o.Locations {
			lines = append(lines, loc+rapid.StringMatching(`(: [a-z ]{0,10})?`).Draw(t, "suffix"))
		}
		lines = append(lines, rapid.SliceOfN(rapid.StringMatching(`  [a-z ]{0,10}`), 0, 2).Draw(t, "noise")...)
		lines = rapid.Permutation(lines).Draw(t, fmt.Sprintf("line order %d", i))
		var details []string
		for len(lines) > 0 {
			n := rapid.IntRange(1, len(lines)).Draw(t, "joined")
			details = append(details, strings.Join(lines[:n], "\n"))
			lines = lines[n:]
		}
		r.Objectives = append(r.Objectives, reportObjective{Objective: o.Objective, Language: o.Language, Status: o.Status, Details: details})
	}
	r.Objectives = rapid.Permutation(r.Objectives).Draw(t, "entry order")
	return r
}

func TestGeneratedExpectationsAreSchemaValid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		data := mustJSON(t, genExpectation(t))
		vs, err := schemas.Validate(schemas.QualExpect, data)
		if err != nil || len(vs) > 0 {
			t.Fatalf("generator produced an invalid expectation %s: %v %v", data, vs, err)
		}
	})
}

func TestReportBuiltFromExpectationMatches(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := genExpectation(t)
		r := reportFrom(t, e)
		if ms := match(e, r); len(ms) > 0 {
			t.Fatalf("expected no mismatches, got %q\nexpectation %+v\nreport %+v", ms, e, r)
		}
	})
}

func indexOf(r report, k entryKey) int {
	return slices.IndexFunc(r.Objectives, func(o reportObjective) bool { return entryKey{o.Objective, o.Language} == k })
}

func requireMismatch(t *rapid.T, ms []string, parts ...string) {
	t.Helper()
	for _, m := range ms {
		if !slices.ContainsFunc(parts, func(p string) bool { return !strings.Contains(m, p) }) {
			return
		}
	}
	t.Fatalf("no mismatch names all of %q; got %q", parts, ms)
}

func keysOf(e expectation) []entryKey {
	keys := make([]entryKey, len(e.Objectives))
	for i, o := range e.Objectives {
		keys[i] = entryKey{o.Objective, o.Language}
	}
	return keys
}

func hasEntries(e expectation) bool { return len(e.Objectives) > 0 }

func hasLocations(e expectation) bool {
	return slices.ContainsFunc(e.Objectives, func(o expectedObjective) bool { return len(o.Locations) > 0 })
}

func always(expectation) bool { return true }

func mutateStatus(t *rapid.T, e expectation, r report) {
	k := rapid.SampledFrom(keysOf(e)).Draw(t, "entry")
	j := indexOf(r, k)
	old := r.Objectives[j].Status
	r.Objectives[j].Status = rapid.SampledFrom(slices.DeleteFunc(slices.Clone(statuses), func(s string) bool { return s == old })).Draw(t, "new status")
	requireMismatch(t, match(e, r), k.String()+":", fmt.Sprintf("%q", old), fmt.Sprintf("%q", r.Objectives[j].Status))
}

func mutateDropEntry(t *rapid.T, e expectation, r report) {
	i := rapid.IntRange(0, len(e.Objectives)-1).Draw(t, "entry")
	k := keysOf(e)[i]
	r.Objectives = slices.Delete(r.Objectives, indexOf(r, k), indexOf(r, k)+1)
	requireMismatch(t, match(e, r), k.String()+":", fmt.Sprintf("%q", e.Objectives[i].Status), "no report entry")
}

func mutateAddEntry(t *rapid.T, e expectation, r report) {
	k := genKey(t, "extra")
	if slices.Contains(keysOf(e), k) {
		t.Skip("drawn key already expected")
	}
	status := rapid.SampledFrom(statuses).Draw(t, "extra status")
	r.Objectives = append(r.Objectives, reportObjective{Objective: k.objective, Language: k.language, Status: status})
	requireMismatch(t, match(e, r), k.String()+":", "no report entry", fmt.Sprintf("%q", status))
}

func mutateLanguage(t *rapid.T, e expectation, r report) {
	k := rapid.SampledFrom(keysOf(e)).Draw(t, "entry")
	j := indexOf(r, k)
	lang := rapid.SampledFrom([]string{"", "go", "ts", "py"}).Filter(func(l string) bool { return l != k.language }).Draw(t, "new language")
	moved := entryKey{k.objective, lang}
	if slices.Contains(keysOf(e), moved) {
		t.Skip("moved key already expected")
	}
	r.Objectives[j].Language = lang
	ms := match(e, r)
	requireMismatch(t, ms, k.String()+":", "no report entry")
	requireMismatch(t, ms, moved.String()+":", "expected no report entry")
}

func mutateLocation(t *rapid.T, e expectation, r report) {
	withLocs := slices.DeleteFunc(slices.Clone(e.Objectives), func(o expectedObjective) bool { return len(o.Locations) == 0 })
	o := rapid.SampledFrom(withLocs).Draw(t, "entry")
	loc := rapid.SampledFrom(o.Locations).Draw(t, "location")
	k := entryKey{o.Objective, o.Language}
	j := indexOf(r, k)
	shift := rapid.SampledFrom([]string{"", "x", " ", "./"}).Draw(t, "shift off line start")
	var kept []string
	for _, l := range detailLines(r.Objectives[j].Details) {
		switch {
		case !strings.HasPrefix(l, loc):
			kept = append(kept, l)
		case shift != "":
			kept = append(kept, shift+l)
		}
	}
	r.Objectives[j].Details = []string{strings.Join(kept, "\n")}
	requireMismatch(t, match(e, r), k.String()+":", fmt.Sprintf("%q", loc), fmt.Sprintf("%q", detailLines(r.Objectives[j].Details)))
}

func mutateProblems(t *rapid.T, e expectation, r report) {
	p := rapid.StringMatching(`[a-z][a-z ]{0,15}`).Draw(t, "problem")
	switch {
	case len(r.Problems) > 0 && rapid.Bool().Draw(t, "drop"):
		r.Problems = r.Problems[1:]
	case len(r.Problems) > 0 && r.Problems[0] != p:
		r.Problems[0] = p
	default:
		r.Problems = append(r.Problems, p)
	}
	requireMismatch(t, match(e, r), "problems:", fmt.Sprintf("%q", e.Problems), fmt.Sprintf("%q", r.Problems))
}

type mutation struct {
	name    string
	applies func(expectation) bool
	apply   func(*rapid.T, expectation, report)
}

var mutations = []mutation{
	{"status", hasEntries, mutateStatus},
	{"drop entry", hasEntries, mutateDropEntry},
	{"add entry", always, mutateAddEntry},
	{"language", hasEntries, mutateLanguage},
	{"location", hasLocations, mutateLocation},
	{"problems", always, mutateProblems},
}

func TestSingleMutationIsReported(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		e := genExpectation(t)
		r := reportFrom(t, e)
		applicable := slices.DeleteFunc(slices.Clone(mutations), func(m mutation) bool { return !m.applies(e) })
		m := rapid.SampledFrom(applicable).Draw(t, "mutation")
		m.apply(t, e, r)
	})
}
