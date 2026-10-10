package core

import (
	"fmt"
	"slices"
	"strings"
)

type ProvRecord struct {
	Session   string
	AgentID   string
	AgentType string
	Tool      string
	Path      string
	Role      Role
	Pre       string
	Post      string
}

type ChainEnds struct {
	Base string
	Head string
}

type Provenance struct {
	Ends     map[string]ChainEnds
	Records  []ProvRecord
	Reviewed bool
	Required map[Level]bool
}

type ProtectedDiff struct {
	Files    []ChangedFile
	Reviewed bool
	Required map[Level]bool
}

type Review struct {
	Login  string
	State  string
	Commit string
}

type Reviews struct {
	Author  string
	Head    string
	Reviews []Review
}

func (r Reviews) Approved() bool {
	latest := map[string]Review{}
	for _, x := range r.Reviews {
		if x.State == "COMMENTED" || x.State == "PENDING" {
			continue
		}
		latest[strings.ToLower(x.Login)] = x
	}
	for login, x := range latest {
		if login != strings.ToLower(r.Author) && x.State == "APPROVED" && x.Commit == r.Head {
			return true
		}
	}
	return false
}

func VerifierSide(agentType string) bool {
	return agentType == Verifier || agentType == Pruner
}

func reach(recs []ProvRecord, start string, edge func(ProvRecord) (string, string)) map[string]bool {
	seen := map[string]bool{start: true}
	for grew := true; grew; {
		grew = false
		for _, r := range recs {
			from, to := edge(r)
			if seen[from] && !seen[to] {
				seen[to] = true
				grew = true
			}
		}
	}
	return seen
}

func Chain(recs []ProvRecord, base, head string) (gap bool, checked []ProvRecord) {
	fwd := reach(recs, base, func(r ProvRecord) (string, string) { return r.Pre, r.Post })
	bwd := reach(recs, head, func(r ProvRecord) (string, string) { return r.Post, r.Pre })
	gap = !fwd[head]
	for _, r := range recs {
		f, b := fwd[r.Pre], bwd[r.Post]
		if (!gap && f && b) || (gap && (f || b)) {
			checked = append(checked, r)
		}
	}
	return gap, checked
}

func AgentConflicts(recs []ProvRecord) []string {
	types := map[string][]string{}
	for _, r := range recs {
		if r.AgentID != "" && !slices.Contains(types[r.AgentID], r.AgentType) {
			types[r.AgentID] = append(types[r.AgentID], r.AgentType)
		}
	}
	var out []string
	for id, ts := range types {
		if len(ts) > 1 {
			slices.Sort(ts)
			out = append(out, fmt.Sprintf("agent_id %s appears with agent types %s", id, strings.Join(ts, ", ")))
		}
	}
	slices.Sort(out)
	return out
}

func unreviewed(path, what string, reviewed bool, required map[Level]bool, level Level) (note string, excused bool) {
	switch {
	case reviewed:
		return fmt.Sprintf("%s: %s, covered by review", path, what), true
	case !required[level]:
		return fmt.Sprintf("%s: unreviewed %s (human_review: optional)", path, what), true
	}
	return "", false
}

func unproducedTier(tier string, required bool) bool {
	return tier == "tier2" || (tier == "tier3" && required)
}

func independenceFindings(o Objective, changed []ChangedFile, pv Provenance) (out []Finding, notes []string) {
	byPath := map[string][]ProvRecord{}
	for _, r := range pv.Records {
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	for _, f := range changed {
		if !inScope(o, f) || o.Levels[f.Level] == "" || strings.HasPrefix(f.Path, ProvenanceDir+"/") {
			continue
		}
		at := func(text string) {
			out = append(out, Finding{Path: f.Path, Level: f.Level, Located: true, Text: text})
		}
		ends, ok := pv.Ends[f.Path]
		if !ok {
			at(fmt.Sprintf("%s: no base and HEAD blobs to chain", f.Path))
			continue
		}
		gap, checked := Chain(byPath[f.Path], ends.Base, ends.Head)
		wantVerifier := f.Role == Test || f.Role == FuzzCorpus
		for _, r := range checked {
			if f.Role != Unclassified && VerifierSide(r.AgentType) != wantVerifier {
				at(fmt.Sprintf("%s: %s wrote this %s file (session %s)", f.Path, actor(r.AgentType), f.Role, r.Session))
			}
		}
		if gap {
			what := "gap (an edit outside the file tools)"
			if note, excused := unreviewed(f.Path, what, pv.Reviewed, pv.Required, f.Level); excused {
				notes = append(notes, note)
			} else {
				at(fmt.Sprintf("%s: %s: no provenance links its base blob to its HEAD blob", f.Path, what))
			}
		}
		if t := o.Independence[f.Level]; unproducedTier(t, pv.Required[f.Level]) {
			at(fmt.Sprintf("%s: level %s needs %s; tier2 evidence is not produced yet", f.Path, f.Level, t))
		}
	}
	slices.SortFunc(out, compareFindings)
	slices.Sort(notes)
	return out, notes
}

func protectedFindings(o Objective, pd ProtectedDiff) (out []Finding, notes []string) {
	for _, f := range pd.Files {
		if o.Levels[f.Level] == "" {
			continue
		}
		if note, excused := unreviewed(f.Path, "protected-file change", pd.Reviewed, pd.Required, f.Level); excused {
			notes = append(notes, note)
			continue
		}
		out = append(out, Finding{Path: f.Path, Level: f.Level, Located: true,
			Text: fmt.Sprintf("%s: protected file changed without a non-author approval", f.Path)})
	}
	slices.SortFunc(out, compareFindings)
	slices.Sort(notes)
	return out, notes
}
