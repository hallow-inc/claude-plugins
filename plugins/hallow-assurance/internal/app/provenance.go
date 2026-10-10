package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	provenanceEvidence = "provenance.chain"
	protectedEvidence  = "protected.diff"
)

func builtin(o core.Objective) bool {
	return o.Evidence == provenanceEvidence || o.Evidence == protectedEvidence
}

var ErrNoPending = errors.New("no pending pre-edit entry")

type pending struct {
	V    int     `json:"v"`
	Path string  `json:"path"`
	Pre  *string `json:"pre"`
}

type record struct {
	V         int     `json:"v"`
	Session   string  `json:"session"`
	AgentID   string  `json:"agent_id,omitempty"`
	AgentType string  `json:"agent_type,omitempty"`
	Tool      string  `json:"tool"`
	Path      string  `json:"path"`
	Role      string  `json:"role"`
	Pre       *string `json:"pre"`
	Post      *string `json:"post"`
}

func blobPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func PendingPath(root, session, toolUseID string) string {
	return filepath.Join(root, core.StateDir, "pending", session, toolUseID+".json")
}

func worktreeBlobs(root string, rels []string) (map[string]string, error) {
	out := map[string]string{}
	var present []string
	for _, rel := range rels {
		ok, err := exists(root, rel)
		if err != nil {
			return nil, err
		}
		if ok {
			present = append(present, rel)
		}
	}
	if len(present) == 0 {
		return out, nil
	}
	hashes, err := git(root, append([]string{"hash-object", "--"}, present...)...)
	if err != nil {
		return nil, err
	}
	if len(hashes) != len(present) {
		return nil, fmt.Errorf("git hash-object returned %d hashes for %d files", len(hashes), len(present))
	}
	for i, rel := range present {
		out[rel] = hashes[i]
	}
	return out, nil
}

func validIDs(ids ...string) bool {
	return !slices.ContainsFunc(ids, func(id string) bool { return !core.ValidSession(id) })
}

func WritePending(m *core.Manifest, session, toolUseID, rel string) error {
	if !validIDs(session, toolUseID) {
		return fmt.Errorf("invalid session or tool use id")
	}
	blobs, err := worktreeBlobs(m.Root, []string{rel})
	if err != nil {
		return err
	}
	data, err := json.Marshal(pending{Path: rel, Pre: blobPtr(blobs[rel])})
	if err != nil {
		return err
	}
	p := PendingPath(m.Root, session, toolUseID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

type RecordInput struct {
	Session   string
	Tool      string
	ToolUseID string
	AgentID   string
	AgentType string
	Path      string
}

func readPending(p string) (pending, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return pending{}, ErrNoPending
	}
	if err != nil {
		return pending{}, err
	}
	if vs, err := schemas.Validate(schemas.Pending, data); err != nil || len(vs) > 0 {
		return pending{}, fmt.Errorf("%s is not a valid pending entry", p)
	}
	var pe pending
	return pe, json.Unmarshal(data, &pe)
}

func Record(m *core.Manifest, in RecordInput) (rel string, appended bool, err error) {
	if !validIDs(in.Session, in.ToolUseID) {
		return "", false, fmt.Errorf("invalid session or tool use id")
	}
	pp := PendingPath(m.Root, in.Session, in.ToolUseID)
	pe, err := readPending(pp)
	if err != nil {
		return "", false, err
	}
	rel = pe.Path
	if in.Path != "" && in.Path != rel {
		return rel, false, fmt.Errorf("pending entry is for %s, not %s", rel, in.Path)
	}
	blobs, err := worktreeBlobs(m.Root, []string{rel})
	if err != nil {
		return rel, false, err
	}
	post := blobs[rel]
	if post == deref(pe.Pre) {
		return rel, false, os.Remove(pp)
	}
	roles, failures, _ := LoadRoles(m)
	if len(failures) > 0 {
		return rel, false, errors.New("cannot determine file roles: " + JoinErrors(failures))
	}
	role, err := roles.Of(rel)
	if err != nil {
		return rel, false, err
	}
	line, err := json.Marshal(record{Session: in.Session, AgentID: in.AgentID, AgentType: in.AgentType, Tool: in.Tool,
		Path: rel, Role: string(role), Pre: pe.Pre, Post: blobPtr(post)})
	if err != nil {
		return rel, false, err
	}
	line = append(line, '\n')
	vs, err := schemas.Validate(schemas.Provenance, line)
	if err != nil {
		return rel, false, fmt.Errorf("record for %s: %w", rel, err)
	}
	if len(vs) > 0 {
		return rel, false, fmt.Errorf("record for %s violates provenance.schema.json: %s", rel, vs[0])
	}
	file := filepath.Join(m.Root, core.ProvenanceDir, in.Session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return rel, false, err
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return rel, false, err
	}
	_, werr := f.Write(line)
	if err := errors.Join(werr, f.Close()); err != nil {
		return rel, false, err
	}
	return rel, true, os.Remove(pp)
}

func parseProvenance(name string, data []byte) ([]core.ProvRecord, error) {
	session, ok := strings.CutSuffix(path.Base(name), ".jsonl")
	if !ok || !core.ValidSession(session) || path.Dir(name) != core.ProvenanceDir {
		return nil, fmt.Errorf("%s: provenance files must be %s/<session>.jsonl", name, core.ProvenanceDir)
	}
	vs, err := schemas.Validate(schemas.Provenance, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("%s: %s", name, vs[0])
	}
	var out []core.ProvRecord
	for i, l := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		var r record
		if err := json.Unmarshal(l, &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, i+1, err)
		}
		if r.Session != session {
			return nil, fmt.Errorf("%s:%d: record session %q does not match the file name", name, i+1, r.Session)
		}
		out = append(out, core.ProvRecord{Session: r.Session, AgentID: r.AgentID, AgentType: r.AgentType, Tool: r.Tool,
			Path: r.Path, Role: core.Role(r.Role), Pre: deref(r.Pre), Post: deref(r.Post)})
	}
	return out, nil
}

func loadProvenance(root string) (recs []core.ProvRecord, files map[string][]byte, problems []string) {
	files = map[string][]byte{}
	dir := filepath.Join(root, core.ProvenanceDir)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = data
		rs, perr := parseProvenance(rel, data)
		if perr != nil {
			problems = append(problems, perr.Error())
			return nil
		}
		recs = append(recs, rs...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		problems = append(problems, err.Error())
	}
	return recs, files, problems
}

func appendOnly(root, base string, head map[string][]byte) ([]string, error) {
	names, err := git(root, "ls-tree", "-r", "--name-only", base, "--", core.ProvenanceDir)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, name := range names {
		old, err := gitBytes(root, "show", base+":./"+name)
		if err != nil {
			return nil, err
		}
		if now, ok := head[name]; !ok || !bytes.HasPrefix(now, old) {
			problems = append(problems, fmt.Sprintf("%s: provenance may only be appended to, but this PR rewrote or removed it", name))
		}
	}
	return problems, nil
}

func baseBlobs(root, base string, rels []string) (map[string]string, error) {
	out := map[string]string{}
	if len(rels) == 0 {
		return out, nil
	}
	lines, err := git(root, append([]string{"ls-tree", "-r", base, "--"}, rels...)...)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		meta, p, ok := strings.Cut(l, "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == "blob" {
			out[p] = f[2]
		}
	}
	return out, nil
}

func baseReviewRequired(root, base string) map[core.Level]bool {
	data, err := gitBytes(root, "show", base+":./"+core.ManifestName)
	if err != nil {
		return nil
	}
	m, err := core.ParseManifest(core.ManifestName+"@"+base, data)
	if err != nil {
		return nil
	}
	return m.ReviewRequired
}

type builtinInputs struct {
	root     string
	base     string
	changed  []core.ChangedFile
	reviewed bool
}

func provenanceEvidenceFor(in builtinInputs) core.Evidence {
	var ev core.Evidence
	recs, files, problems := loadProvenance(in.root)
	ev.Problems = append(ev.Problems, problems...)
	ap, err := appendOnly(in.root, in.base, files)
	if err != nil {
		ev.Problems = append(ev.Problems, err.Error())
	}
	ev.Problems = append(ev.Problems, ap...)
	ev.Problems = append(ev.Problems, core.AgentConflicts(recs)...)
	rels := make([]string, len(in.changed))
	for i, f := range in.changed {
		rels[i] = f.Path
	}
	bb, err := baseBlobs(in.root, in.base, rels)
	if err != nil {
		ev.Problems = append(ev.Problems, err.Error())
		return ev
	}
	hb, err := worktreeBlobs(in.root, rels)
	if err != nil {
		ev.Problems = append(ev.Problems, err.Error())
		return ev
	}
	pv := core.Provenance{Ends: map[string]core.ChainEnds{}, Records: recs, Reviewed: in.reviewed,
		Required: baseReviewRequired(in.root, in.base)}
	for _, p := range rels {
		pv.Ends[p] = core.ChainEnds{Base: bb[p], Head: hb[p]}
	}
	ev.Provenance = &pv
	return ev
}

func protectedEvidenceFor(m *core.Manifest, in builtinInputs) core.Evidence {
	pd := core.ProtectedDiff{Reviewed: in.reviewed, Required: baseReviewRequired(in.root, in.base)}
	for _, f := range in.changed {
		p := f.Path
		if strings.HasPrefix(p, core.StateDir+"/") ||
			(path.Dir(p) == core.ProvenanceDir && strings.HasSuffix(p, ".jsonl")) {
			continue
		}
		if m.IsProtected(p) {
			pd.Files = append(pd.Files, f)
		}
	}
	return core.Evidence{Protected: &pd}
}

func LoadReviews(m *core.Manifest, file string) (*core.Reviews, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	vs, err := schemas.Validate(schemas.Reviews, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(vs) > 0 {
		return nil, &core.LoadError{File: file, Problems: vs}
	}
	var doc struct {
		Author  string `json:"author"`
		Head    string `json:"head"`
		Reviews []struct {
			Login  string `json:"login"`
			State  string `json:"state"`
			Commit string `json:"commit"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	head, err := revParse(m.Root, "HEAD")
	if err != nil {
		return nil, err
	}
	if doc.Head != head {
		return nil, fmt.Errorf("%s: head %s is not HEAD (%s)", file, doc.Head, head)
	}
	r := &core.Reviews{Author: doc.Author, Head: doc.Head}
	for _, x := range doc.Reviews {
		r.Reviews = append(r.Reviews, core.Review{Login: x.Login, State: x.State, Commit: x.Commit})
	}
	return r, nil
}
