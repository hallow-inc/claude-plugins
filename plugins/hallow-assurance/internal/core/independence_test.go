package core

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var (
	indBlobs    = []string{"", "b0", "b1", "b2", "b3"}
	indAgents   = []string{Implementer, Verifier, Pruner, Inspector, "", "someone:else"}
	indRoles    = []Role{Source, Test, Generated, FuzzCorpus, Config, Unclassified}
	indPaths    = []string{"pkg/a.go", "pkg/a_test.go", "pkg/b.go", "testdata/fuzz/x", "README.md"}
	indSessions = []string{"s1", "s2", "s3"}
)

func catalogObjective(t testing.TB, id string) Objective {
	c, err := LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range c.Objectives {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("catalog v0 has no %s", id)
	return Objective{}
}

func rec(path, agentType, pre, post string) ProvRecord {
	return ProvRecord{Session: "s1", AgentType: agentType, Tool: "Edit", Path: path, Role: Source, Pre: pre, Post: post}
}

func recKey(r ProvRecord) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s", r.Path, r.AgentID, r.AgentType, r.Role, r.Pre, r.Post)
}

func recKeys(rs []ProvRecord) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = recKey(r)
	}
	slices.Sort(out)
	return out
}

func recordGen(path string) *rapid.Generator[ProvRecord] {
	return rapid.Custom(func(t *rapid.T) ProvRecord {
		r := ProvRecord{
			Session:   rapid.SampledFrom(indSessions).Draw(t, "session"),
			AgentType: rapid.SampledFrom(indAgents).Draw(t, "agent"),
			Tool:      "Edit",
			Path:      path,
			Role:      rapid.SampledFrom(indRoles).Draw(t, "recrole"),
			Pre:       rapid.SampledFrom(indBlobs).Draw(t, "pre"),
			Post:      rapid.SampledFrom(indBlobs).Draw(t, "post"),
		}
		if r.Pre == "" && r.Post == "" {
			r.Post = "b1"
		}
		if r.AgentType != "" {
			r.AgentID = "id-" + r.AgentType
		}
		return r
	})
}

// closure is the reflexive-transitive closure of the record edges, by
// Floyd-Warshall, so the oracle shares no search code with Chain.
func closure(recs []ProvRecord) func(from, to string) bool {
	idx := map[string]int{}
	for _, b := range indBlobs {
		idx[b] = len(idx)
	}
	n := len(idx)
	m := make([][]bool, n)
	for i := range m {
		m[i] = make([]bool, n)
		m[i][i] = true
	}
	for _, r := range recs {
		m[idx[r.Pre]][idx[r.Post]] = true
	}
	for k := range n {
		for i := range n {
			for j := range n {
				m[i][j] = m[i][j] || (m[i][k] && m[k][j])
			}
		}
	}
	return func(from, to string) bool { return m[idx[from]][idx[to]] }
}

func chainOracle(recs []ProvRecord, base, head string) (gap bool, checked []ProvRecord) {
	r := closure(recs)
	gap = !r(base, head)
	for _, x := range recs {
		fromBase, toHead := r(base, x.Pre), r(x.Post, head)
		if (gap && (fromBase || toHead)) || (!gap && fromBase && toHead) {
			checked = append(checked, x)
		}
	}
	return gap, checked
}

func TestChainScenarios(t *testing.T) {
	p := "pkg/a.go"
	abandoned := []ProvRecord{rec(p, Implementer, "b0", "b1"), rec(p, Verifier, "b0", "bX"), rec(p, Implementer, "b1", "b2")}
	split := []ProvRecord{rec(p, Implementer, "b0", "b1"), rec(p, Verifier, "b2", "b3"), rec(p, Implementer, "b4", "b5")}
	cases := []struct {
		name        string
		recs        []ProvRecord
		base, head  string
		gap         bool
		wantChecked []ProvRecord
	}{
		{"unbroken chain checks both records", []ProvRecord{rec(p, "", "b0", "b1"), rec(p, "", "b1", "b2")}, "b0", "b2", false,
			[]ProvRecord{rec(p, "", "b0", "b1"), rec(p, "", "b1", "b2")}},
		{"a HEAD blob no record produced is a gap", []ProvRecord{rec(p, "", "b0", "b1")}, "b0", "b3", true,
			[]ProvRecord{rec(p, "", "b0", "b1")}},
		{"a chain reaching HEAD but not from base is a gap", []ProvRecord{rec(p, "", "b1", "b2")}, "b0", "b2", true,
			[]ProvRecord{rec(p, "", "b1", "b2")}},
		{"an abandoned branch is ignored", abandoned, "b0", "b2", false, []ProvRecord{abandoned[0], abandoned[2]}},
		{"a gap checks records reachable from base or leading to HEAD", split, "b0", "b3", true, split[:2]},
		{"creation then deletion chains through null", []ProvRecord{rec(p, "", "", "b1"), rec(p, "", "b1", "")}, "", "", false,
			[]ProvRecord{rec(p, "", "", "b1"), rec(p, "", "b1", "")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gap, checked := Chain(c.recs, c.base, c.head)
			if gap != c.gap || !slices.Equal(recKeys(checked), recKeys(c.wantChecked)) {
				t.Fatalf("gap=%v checked=%v, want gap=%v checked=%v", gap, recKeys(checked), c.gap, recKeys(c.wantChecked))
			}
		})
	}
}

func TestChainMatchesClosureOracleAndIgnoresOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		recs := rapid.SliceOfN(recordGen("p"), 0, 7).Draw(t, "recs")
		base := rapid.SampledFrom(indBlobs).Draw(t, "base")
		head := rapid.SampledFrom(indBlobs).Draw(t, "head")
		gap, checked := Chain(recs, base, head)
		wantGap, wantChecked := chainOracle(recs, base, head)
		if gap != wantGap || !slices.Equal(recKeys(checked), recKeys(wantChecked)) {
			t.Fatalf("Chain = (%v, %v), oracle = (%v, %v)", gap, recKeys(checked), wantGap, recKeys(wantChecked))
		}
		resplit := rapid.Permutation(recs).Draw(t, "order")
		for i := range resplit {
			resplit[i].Session = rapid.SampledFrom(indSessions).Draw(t, "resession")
		}
		gap2, checked2 := Chain(resplit, base, head)
		if gap2 != gap || !slices.Equal(recKeys(checked2), recKeys(checked)) {
			t.Fatalf("reordering and re-splitting records across sessions changed the result: (%v, %v) vs (%v, %v)",
				gap, recKeys(checked), gap2, recKeys(checked2))
		}
	})
}

type indWorld struct {
	changed []ChangedFile
	pv      Provenance
}

func allRequired() map[Level]bool {
	m := map[Level]bool{}
	for _, l := range ruleLevels {
		m[l] = true
	}
	return m
}

func indWorldGen(t *rapid.T) indWorld {
	var w indWorld
	w.pv.Ends = map[string]ChainEnds{}
	w.pv.Required = map[Level]bool{}
	for _, p := range indPaths {
		if rapid.Bool().Draw(t, "changed "+p) {
			w.changed = append(w.changed, ChangedFile{
				Path:  p,
				Level: rapid.SampledFrom(ruleLevels).Draw(t, "level"),
				Role:  rapid.SampledFrom(indRoles).Draw(t, "role"),
			})
			if rapid.IntRange(0, 9).Draw(t, "hasEnds") > 0 {
				w.pv.Ends[p] = ChainEnds{Base: rapid.SampledFrom(indBlobs).Draw(t, "base"), Head: rapid.SampledFrom(indBlobs).Draw(t, "head")}
			}
		}
		w.pv.Records = append(w.pv.Records, rapid.SliceOfN(recordGen(p), 0, 5).Draw(t, "recs "+p)...)
	}
	w.pv.Records = rapid.Permutation(w.pv.Records).Draw(t, "order")
	w.pv.Reviewed = rapid.Bool().Draw(t, "reviewed")
	for _, l := range ruleLevels {
		w.pv.Required[l] = rapid.Bool().Draw(t, "required"+string(l))
	}
	return w
}

func (w indWorld) chainOf(path string) (gap bool, checked []ProvRecord, ok bool) {
	ends, ok := w.pv.Ends[path]
	if !ok {
		return false, nil, false
	}
	var mine []ProvRecord
	for _, r := range w.pv.Records {
		if r.Path == path {
			mine = append(mine, r)
		}
	}
	gap, checked = chainOracle(mine, ends.Base, ends.Head)
	return gap, checked, true
}

func (w indWorld) unverifiedLevelBTest() (agentType, path string, found bool) {
	for _, f := range w.changed {
		if f.Level != "B" || f.Role != Test {
			continue
		}
		_, checked, _ := w.chainOf(f.Path)
		for _, r := range checked {
			if r.AgentType != Verifier && r.AgentType != Pruner {
				return r.AgentType, f.Path, true
			}
		}
	}
	return "", "", false
}

type indExpect struct {
	failing    map[string]bool
	noted      map[string]bool
	violations map[string]int
}

func (w indWorld) expect(o Objective) indExpect {
	e := indExpect{failing: map[string]bool{}, noted: map[string]bool{}, violations: map[string]int{}}
	for _, f := range w.changed {
		if o.Levels[f.Level] == "" {
			continue
		}
		gap, checked, ok := w.chainOf(f.Path)
		if !ok {
			e.failing[f.Path] = true
			continue
		}
		testLike := f.Role == Test || f.Role == FuzzCorpus
		for _, r := range checked {
			if f.Role != Unclassified && (r.AgentType == Verifier || r.AgentType == Pruner) != testLike {
				e.failing[f.Path] = true
				e.violations[f.Path]++
			}
		}
		if gap {
			if w.pv.Reviewed || !w.pv.Required[f.Level] {
				e.noted[f.Path] = true
			} else {
				e.failing[f.Path] = true
			}
		}
		if tier := o.Independence[f.Level]; tier == "tier2" || (tier == "tier3" && w.pv.Required[f.Level]) {
			e.failing[f.Path] = true
		}
	}
	return e
}

func (w indWorld) decide(o Objective) Outcome {
	pv := w.pv
	return DecideObjective(o, w.changed, Evidence{Provenance: &pv}, nil, nil, ruleDate)
}

func isNote(line string) bool {
	return strings.Contains(line, "covered by review") || strings.Contains(line, "(human_review: optional)")
}

type detailSplit struct {
	failing    map[string]bool
	noted      map[string]bool
	violations map[string]int
}

func splitDetails(t interface{ Fatalf(string, ...any) }, details []string, paths []string) detailSplit {
	s := detailSplit{failing: map[string]bool{}, noted: map[string]bool{}, violations: map[string]int{}}
	for _, line := range details {
		i := slices.IndexFunc(paths, func(p string) bool { return strings.HasPrefix(line, p+": ") })
		if i < 0 {
			t.Fatalf("detail %q names no changed file", line)
		}
		switch {
		case isNote(line):
			s.noted[paths[i]] = true
		default:
			s.failing[paths[i]] = true
			if strings.Contains(line, " wrote this ") {
				s.violations[paths[i]]++
			}
		}
	}
	return s
}

func TestIndependenceMatchesTheTier1Oracle(t *testing.T) {
	o := catalogObjective(t, "IND-VERIFIER-DISTINCT")
	rapid.Check(t, func(t *rapid.T) {
		w := indWorldGen(t)
		got := w.decide(o)
		e := w.expect(o)
		want := Pass
		if len(e.failing) > 0 {
			want = Fail
		}
		if got.Status != want {
			t.Fatalf("status %s, oracle %s (failing %v)\n%s", got.Status, want, e.failing, strings.Join(got.Details, "\n"))
		}
		s := splitDetails(t, got.Details, indPaths)
		if !maps.Equal(s.failing, e.failing) || !maps.Equal(s.noted, e.noted) || !maps.Equal(s.violations, e.violations) {
			t.Fatalf("details disagree with the oracle:\n got failing=%v noted=%v violations=%v\nwant failing=%v noted=%v violations=%v\n%s",
				s.failing, s.noted, s.violations, e.failing, e.noted, e.violations, strings.Join(got.Details, "\n"))
		}
		if who, path, ok := w.unverifiedLevelBTest(); ok && got.Status != Fail {
			t.Fatalf("%s authored level-B test %s on the checked chain, yet the objective is %s", actor(who), path, got.Status)
		}
		shuffled := w
		shuffled.pv.Records = rapid.Permutation(w.pv.Records).Draw(t, "reorder")
		if again := shuffled.decide(o); again.Status != got.Status || !slices.Equal(again.Details, got.Details) {
			t.Fatalf("record order changed the outcome:\n%v\n%v", got.Details, again.Details)
		}
	})
}

func TestOptionalReviewOnlyExcusesGapsAndProtectedChanges(t *testing.T) {
	ind := catalogObjective(t, "IND-VERIFIER-DISTINCT")
	prot := catalogObjective(t, "CFG-PROTECTED")
	failLines := func(d []string) []string {
		return slices.DeleteFunc(slices.Clone(d), isNote)
	}
	roleLines := func(d []string) []string {
		return slices.DeleteFunc(slices.Clone(d), func(s string) bool { return !strings.Contains(s, " wrote this ") })
	}
	rapid.Check(t, func(t *rapid.T) {
		w := indWorldGen(t)
		strict := w
		strict.pv.Required = allRequired()
		relaxed, base := w.decide(ind), strict.decide(ind)
		if base.Status == Pass && relaxed.Status != Pass {
			t.Fatalf("optional turned a pass into %s", relaxed.Status)
		}
		if !slices.Equal(roleLines(relaxed.Details), roleLines(base.Details)) {
			t.Fatalf("optional changed role-violation lines:\n%v\n%v", roleLines(base.Details), roleLines(relaxed.Details))
		}
		for _, line := range failLines(relaxed.Details) {
			if !slices.Contains(base.Details, line) {
				t.Fatalf("optional introduced failure %q", line)
			}
		}
		for _, line := range failLines(base.Details) {
			if slices.Contains(relaxed.Details, line) || strings.Contains(line, "gap") || strings.Contains(line, "needs tier3") {
				continue
			}
			t.Fatalf("optional removed a failure that is neither a gap nor tier3's approval %q", line)
		}
		checkProtectedOptional(t, prot, w)
	})
}

func checkProtectedOptional(t *rapid.T, prot Objective, w indWorld) {
	var files []ChangedFile
	for _, f := range w.changed {
		files = append(files, ChangedFile{Path: f.Path, Level: f.Level})
	}
	pd := ProtectedDiff{Files: files, Reviewed: w.pv.Reviewed, Required: allRequired()}
	strict := DecideObjective(prot, files, Evidence{Protected: &pd}, nil, nil, ruleDate)
	pd.Required = w.pv.Required
	relaxed := DecideObjective(prot, files, Evidence{Protected: &pd}, nil, nil, ruleDate)
	s := splitDetails(t, relaxed.Details, indPaths)
	for _, f := range files {
		excused := pd.Reviewed || !pd.Required[f.Level]
		if s.failing[f.Path] == excused || s.noted[f.Path] != excused {
			t.Fatalf("protected %s at %s (reviewed=%v required=%v): details %v", f.Path, f.Level, pd.Reviewed, pd.Required, relaxed.Details)
		}
	}
	if strict.Status == Pass && relaxed.Status != Pass {
		t.Fatalf("optional turned a protected pass into %s", relaxed.Status)
	}
}

var (
	indSrc = ChangedFile{Path: "pkg/a.go", Level: "B", Role: Source}
	indTst = ChangedFile{Path: "pkg/a_test.go", Level: "B", Role: Test}
	indA   = ChangedFile{Path: "pkg/a_test.go", Level: "A", Role: Test}
	indFix = ChangedFile{Path: "testdata/hooks/x.json", Level: "B", Role: Unclassified}
)

func indEnds(base, head string, files ...ChangedFile) map[string]ChainEnds {
	m := map[string]ChainEnds{}
	for _, f := range files {
		m[f.Path] = ChainEnds{Base: base, Head: head}
	}
	return m
}

func one(f ChangedFile, agentType, pre, post string) []ProvRecord {
	return []ProvRecord{rec(f.Path, agentType, pre, post)}
}

func TestIndependenceScenarios(t *testing.T) {
	o := catalogObjective(t, "IND-VERIFIER-DISTINCT")
	reqA, reqB := map[Level]bool{"A": true}, map[Level]bool{"B": true}
	recordRoleSource := one(indTst, Implementer, "b0", "b1")
	recordRoleSource[0].Role = Source
	cases := []struct {
		name    string
		files   []ChangedFile
		pv      Provenance
		want    Status
		mention []string
	}{
		{"implementer wrote the test", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "b1", indTst), Records: one(indTst, Implementer, "b0", "b1")}, Fail,
			[]string{indTst.Path, Implementer, "session s1"}},
		{"main thread wrote the test", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "b1", indTst), Records: one(indTst, "", "b0", "b1")}, Fail, []string{indTst.Path, "main thread"}},
		{"verifier wrote source", []ChangedFile{indSrc},
			Provenance{Ends: indEnds("b0", "b1", indSrc), Records: one(indSrc, Verifier, "b0", "b1")}, Fail, []string{indSrc.Path, Verifier}},
		{"independent authorship passes", []ChangedFile{indSrc, indTst},
			Provenance{Ends: indEnds("b0", "b1", indSrc, indTst), Records: append(one(indSrc, "", "b0", "b1"), one(indTst, Verifier, "b0", "b1")...)}, Pass, nil},
		{"a pruner deleting a test is verifier-side (D5)", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "", indTst), Records: one(indTst, Pruner, "b0", "")}, Pass, nil},
		{"the role comes from the changed file, not the record", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "b1", indTst), Records: recordRoleSource}, Fail, []string{Implementer}},
		{"level A change, review required, needs tier3", []ChangedFile{indA},
			Provenance{Ends: indEnds("b0", "b1", indA), Records: one(indA, Verifier, "b0", "b1"), Required: reqA}, Fail, []string{indA.Path, "tier3"}},
		{"level A change, review optional, evaluates as tier1", []ChangedFile{indA},
			Provenance{Ends: indEnds("b0", "b1", indA), Records: one(indA, Verifier, "b0", "b1")}, Pass, nil},
		{"level A role violation, review optional, fails", []ChangedFile{indA},
			Provenance{Ends: indEnds("b0", "b1", indA), Records: one(indA, Implementer, "b0", "b1")}, Fail, []string{indA.Path, Implementer}},
		{"a file without chain ends fails closed", []ChangedFile{indSrc}, Provenance{Ends: map[string]ChainEnds{}}, Fail, []string{indSrc.Path}},
		{"gap at a review-required level fails", []ChangedFile{indSrc},
			Provenance{Ends: indEnds("b0", "b3", indSrc), Records: one(indSrc, "", "b0", "b1"), Required: reqB}, Fail, []string{indSrc.Path, "gap"}},
		{"gap at a review-optional level passes and is listed unreviewed", []ChangedFile{indSrc},
			Provenance{Ends: indEnds("b0", "b3", indSrc), Records: one(indSrc, "", "b0", "b1")}, Pass,
			[]string{indSrc.Path, "unreviewed", "human_review: optional"}},
		{"gap does not hide the author", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "b3", indTst), Records: one(indTst, Implementer, "b0", "b1")}, Fail, []string{Implementer}},
		{"optional review does not excuse a role violation", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "b1", indTst), Records: one(indTst, Implementer, "b0", "b1")}, Fail, []string{Implementer}},
		{"owner hand-edit with approval passes and notes the review", []ChangedFile{indSrc},
			Provenance{Ends: indEnds("b0", "b3", indSrc), Records: one(indSrc, "", "b0", "b1"), Reviewed: true, Required: reqB}, Pass,
			[]string{indSrc.Path, "covered by review"}},
		{"verifier wrote an unclassified fixture", []ChangedFile{indFix},
			Provenance{Ends: indEnds("b0", "b1", indFix), Records: one(indFix, Verifier, "b0", "b1")}, Pass, nil},
		{"an unclassified fixture's gap still fails where review is required", []ChangedFile{indFix},
			Provenance{Ends: indEnds("b0", "b3", indFix), Records: one(indFix, Verifier, "b0", "b1"), Required: reqB}, Fail, []string{indFix.Path, "gap"}},
		{"review does not excuse a role violation", []ChangedFile{indTst},
			Provenance{Ends: indEnds("b0", "b1", indTst), Records: one(indTst, Implementer, "b0", "b1"), Reviewed: true}, Fail, []string{Implementer}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pv := c.pv
			got := DecideObjective(o, c.files, Evidence{Provenance: &pv}, nil, nil, ruleDate)
			d := strings.Join(got.Details, "\n")
			if got.Status != c.want {
				t.Fatalf("status %s, want %s\n%s", got.Status, c.want, d)
			}
			for _, m := range c.mention {
				if !strings.Contains(d, m) {
					t.Fatalf("details do not mention %q:\n%s", m, d)
				}
			}
		})
	}
}

func TestAgentConflicts(t *testing.T) {
	a, b := rec("x", Verifier, "b0", "b1"), rec("y", Implementer, "b0", "b1")
	a.AgentID, b.AgentID = "abc", "abc"
	if got := AgentConflicts([]ProvRecord{a, b}); len(got) != 1 || !strings.Contains(got[0], "abc") ||
		!strings.Contains(got[0], Verifier) || !strings.Contains(got[0], Implementer) {
		t.Fatalf("one agent_id with two agent types must be reported naming both; got %v", got)
	}
	rapid.Check(t, func(t *rapid.T) {
		var recs []ProvRecord
		for range rapid.IntRange(0, 8).Draw(t, "n") {
			r := rec("p", rapid.SampledFrom(indAgents).Draw(t, "type"), "b0", "b1")
			r.AgentID = rapid.SampledFrom([]string{"", "x", "y", "z"}).Draw(t, "id")
			recs = append(recs, r)
		}
		want := map[string]bool{}
		for _, a := range recs {
			for _, b := range recs {
				if a.AgentID != "" && a.AgentID == b.AgentID && a.AgentType != b.AgentType {
					want[a.AgentID] = true
				}
			}
		}
		got := AgentConflicts(recs)
		if len(got) != len(want) {
			t.Fatalf("got %v, want conflicts for %v", got, want)
		}
		for _, line := range got {
			if !slices.ContainsFunc(slices.Collect(maps.Keys(want)), func(id string) bool { return strings.Contains(line, "agent_id "+id+" ") }) {
				t.Fatalf("conflict %q names no conflicting id (want %v)", line, want)
			}
		}
	})
}

const (
	headSHA  = "0123456789abcdef0123456789abcdef01234567"
	olderSHA = "89abcdef0123456789abcdef0123456789abcdef"
)

func approvedOracle(r Reviews) bool {
	logins := map[string]bool{}
	for _, x := range r.Reviews {
		logins[strings.ToLower(x.Login)] = true
	}
	for login := range logins {
		if login == strings.ToLower(r.Author) {
			continue
		}
		for i := len(r.Reviews) - 1; i >= 0; i-- {
			x := r.Reviews[i]
			if strings.ToLower(x.Login) != login || x.State == "COMMENTED" || x.State == "PENDING" {
				continue
			}
			if x.State == "APPROVED" && x.Commit == r.Head {
				return true
			}
			break
		}
	}
	return false
}

func TestApprovedMatchesTheQualifyingRule(t *testing.T) {
	logins := []string{"octo", "Octo", "OCTO", "alice", "Alice", "bob"}
	states := []string{"APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING"}
	rapid.Check(t, func(t *rapid.T) {
		r := Reviews{Author: rapid.SampledFrom(logins).Draw(t, "author"), Head: headSHA}
		for range rapid.IntRange(0, 6).Draw(t, "n") {
			r.Reviews = append(r.Reviews, Review{
				Login:  rapid.SampledFrom(logins).Draw(t, "login"),
				State:  rapid.SampledFrom(states).Draw(t, "state"),
				Commit: rapid.SampledFrom([]string{headSHA, olderSHA}).Draw(t, "commit"),
			})
		}
		if got, want := r.Approved(), approvedOracle(r); got != want {
			t.Fatalf("Approved() = %v, rule says %v for %+v", got, want, r)
		}
	})
}

func TestApprovalScenarios(t *testing.T) {
	approve := func(login, commit string) Review { return Review{Login: login, State: "APPROVED", Commit: commit} }
	cases := map[string]struct {
		r    Reviews
		want bool
	}{
		"author approves own PR":                {Reviews{"octo", headSHA, []Review{approve("octo", headSHA)}}, false},
		"author approves under another case":    {Reviews{"octo", headSHA, []Review{approve("OCTO", headSHA)}}, false},
		"approval on an older commit":           {Reviews{"octo", headSHA, []Review{approve("alice", olderSHA)}}, false},
		"comment after approval still counts":   {Reviews{"octo", headSHA, []Review{approve("alice", headSHA), {"alice", "COMMENTED", headSHA}}}, true},
		"changes requested withdraws approval":  {Reviews{"octo", headSHA, []Review{approve("alice", headSHA), {"alice", "CHANGES_REQUESTED", headSHA}}}, false},
		"withdrawal under another login casing": {Reviews{"octo", headSHA, []Review{approve("alice", headSHA), {"ALICE", "DISMISSED", headSHA}}}, false},
		"no reviews":                            {Reviews{"octo", headSHA, nil}, false},
	}
	for name, c := range cases {
		if got := c.r.Approved(); got != c.want {
			t.Errorf("%s: Approved() = %v, want %v", name, got, c.want)
		}
	}
}

func TestProtectedDiffScenarios(t *testing.T) {
	o := catalogObjective(t, "CFG-PROTECTED")
	reqB := map[Level]bool{"B": true}
	waivers := []ChangedFile{{Path: ".assure/waivers.yaml", Level: "B"}}
	manifest := []ChangedFile{{Path: "assurance.yaml", Level: "B"}}
	cases := []struct {
		name    string
		pd      ProtectedDiff
		want    Status
		mention []string
	}{
		{"waiver edited without review fails naming the path", ProtectedDiff{Files: waivers, Required: reqB}, Fail, []string{".assure/waivers.yaml: "}},
		{"turning review off is judged by the base setting, required", ProtectedDiff{Files: manifest, Required: reqB}, Fail, []string{"assurance.yaml: "}},
		{"waiver edited with approval passes, listed as covered by review", ProtectedDiff{Files: waivers, Reviewed: true, Required: reqB}, Pass,
			[]string{".assure/waivers.yaml: ", "covered by review"}},
		{"protected change at a review-optional level passes as unreviewed", ProtectedDiff{Files: waivers}, Pass,
			[]string{".assure/waivers.yaml: ", "unreviewed", "human_review: optional"}},
		{"required at another level leaves B optional", ProtectedDiff{Files: waivers, Required: map[Level]bool{"C": true}}, Pass,
			[]string{".assure/waivers.yaml: ", "unreviewed", "human_review: optional"}},
		{"no protected change passes", ProtectedDiff{}, Pass, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pd := c.pd
			got := DecideObjective(o, pd.Files, Evidence{Protected: &pd}, nil, nil, ruleDate)
			d := strings.Join(got.Details, "\n")
			if got.Status != c.want {
				t.Fatalf("status %s, want %s\n%s", got.Status, c.want, d)
			}
			for _, m := range c.mention {
				if !strings.Contains(d, m) {
					t.Fatalf("details do not mention %q:\n%s", m, d)
				}
			}
			if len(c.mention) == 0 && d != "" {
				t.Fatalf("no protected change should leave no details, got %q", d)
			}
		})
	}
}

func TestHumanReviewIsReadFromTheManifest(t *testing.T) {
	m, err := parseManifest("m.yaml", []byte(manifestYAML()))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ReviewRequired) != 0 {
		t.Fatalf("absent human_review must mean optional at every level; got required at %v", m.ReviewRequired)
	}
	rapid.Check(t, func(t *rapid.T) {
		indSrc := manifestYAML() + "human_review:\n"
		want := map[Level]bool{}
		some := false
		for _, l := range ruleLevels {
			switch rapid.SampledFrom([]string{"", "required", "optional"}).Draw(t, "hr"+string(l)) {
			case "required":
				want[l] = true
				indSrc += "  " + string(l) + ": required\n"
				some = true
			case "optional":
				indSrc += "  " + string(l) + ": optional\n"
				some = true
			}
		}
		if !some {
			indSrc += "  {}\n"
		}
		m, err := parseManifest("m.yaml", []byte(indSrc))
		if err != nil {
			t.Fatalf("%v\n%s", err, indSrc)
		}
		for _, l := range ruleLevels {
			if m.ReviewRequired[l] != want[l] {
				t.Fatalf("level %s required = %v, want %v\n%s", l, m.ReviewRequired[l], want[l], indSrc)
			}
		}
	})
}
