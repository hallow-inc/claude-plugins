package app

import (
	"context"
	"crypto/sha1" //nolint:gosec // git object ids are SHA-1; this reproduces them independently of git
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const provManifest = "version: 0\ncatalog: v0\nlanguages: [xx]\ndefault_level: B\ncomponents: []\nprotected: ['docs/**']\n"

const provManifestOptional = provManifest + "human_review: {B: optional}\n"

func xxOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
describe) printf '%s' '{"protocol":1,"languages":["xx"],"claims":["**/*.xx"],"patterns":{"test":["**/*_test.xx"]},"objectives":{}}' ;;
*) echo "assure-adapter-xx: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "assure-adapter-xx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	isolateGit(t)
}

func gitHEAD(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

type provRepo struct {
	root  string
	reset func(t fataler)
}

func newProvRepo(t *testing.T, manifest string, seed map[string]string) provRepo {
	t.Helper()
	xxOnPath(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	putFile(t, root, "assurance.yaml", manifest)
	putFile(t, root, ".gitignore", ".assure/state/\n")
	putFile(t, root, "p/x.xx", "one\n")
	putFile(t, root, "p/x_test.xx", "check one\n")
	for p, c := range seed {
		putFile(t, root, p, c)
	}
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "base")
	return provRepo{root: root, reset: func(t fataler) {
		gitIn(t, root, "reset", "-q", "--hard", "main")
		gitIn(t, root, "clean", "-fdq")
	}}
}

func blobOf(content *string) string {
	if content == nil {
		return ""
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(*content), *content)))
	return hex.EncodeToString(sum[:])
}

func strp(s string) *string { return &s }

func samePtr(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

type chainModel struct {
	base, cur map[string]*string
	want      []core.ProvRecord
	touched   map[string]bool
}

func newChainModel() *chainModel {
	base := map[string]*string{"p/x.xx": strp("one\n"), "p/x_test.xx": strp("check one\n"), "p/new.xx": nil}
	cur := map[string]*string{}
	for p, c := range base {
		cur[p] = c
	}
	return &chainModel{base: base, cur: cur, touched: map[string]bool{}}
}

func drawEdit(t *rapid.T) (rel, session, agent string, after *string) {
	rel = rapid.SampledFrom([]string{"p/x.xx", "p/x_test.xx", "p/new.xx"}).Draw(t, "path")
	session = rapid.SampledFrom([]string{"s1", "s2"}).Draw(t, "session")
	agent = rapid.SampledFrom([]string{"", core.Implementer, core.Verifier}).Draw(t, "agent")
	if !rapid.Bool().Draw(t, "delete") {
		after = strp(rapid.SampledFrom([]string{"a\n", "b\n", "one\n"}).Draw(t, "body"))
	}
	return rel, session, agent, after
}

func (c *chainModel) edit(t *rapid.T, pr provRepo, m *core.Manifest, id string) {
	rel, session, agent, after := drawEdit(t)
	if err := WritePending(m, session, id, rel); err != nil {
		t.Fatalf("%v", err)
	}
	if after == nil {
		_ = os.Remove(filepath.Join(pr.root, rel))
	} else {
		putFile(t, pr.root, rel, *after)
	}
	in := RecordInput{Session: session, Tool: "Edit", ToolUseID: id, AgentType: agent}
	if agent != "" {
		in.AgentID = "id-" + agent
	}
	if rapid.Bool().Draw(t, "name path") {
		in.Path = rel
	}
	gotRel, appended, err := Record(m, in)
	if err != nil {
		t.Fatalf("Record %s: %v", rel, err)
	}
	changed := !samePtr(c.cur[rel], after)
	if gotRel != rel || appended != changed {
		t.Fatalf("Record %s: rel %q appended %v, want appended %v (before %v after %v)", rel, gotRel, appended, changed, c.cur[rel], after)
	}
	if _, err := os.Stat(PendingPath(pr.root, session, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending entry for %s survived Record: %v", rel, err)
	}
	if changed {
		role := core.Role("source")
		if strings.HasSuffix(rel, "_test.xx") {
			role = "test"
		}
		c.want = append(c.want, core.ProvRecord{Session: session, AgentID: in.AgentID, AgentType: agent, Tool: "Edit",
			Path: rel, Role: role, Pre: blobOf(c.cur[rel]), Post: blobOf(after)})
		c.touched[rel] = true
	}
	c.cur[rel] = after
}

func (c *chainModel) checkRecords(t *rapid.T, root string) {
	recs, _, problems := loadProvenance(root)
	if len(problems) > 0 {
		t.Fatalf("recorded provenance does not load: %v", problems)
	}
	for _, s := range []string{"s1", "s2"} {
		bySession := func(rs []core.ProvRecord) []core.ProvRecord {
			return slices.DeleteFunc(slices.Clone(rs), func(r core.ProvRecord) bool { return r.Session != s })
		}
		if got, w := bySession(recs), bySession(c.want); !slices.Equal(got, w) {
			t.Fatalf("session %s records\n got %+v\nwant %+v", s, got, w)
		}
	}
}

func (c *chainModel) checkEvidence(t *rapid.T, root string) {
	var changed []core.ChangedFile
	for p := range c.touched {
		changed = append(changed, core.ChangedFile{Path: p})
	}
	reviewed := rapid.Bool().Draw(t, "reviewed")
	ev := provenanceEvidenceFor(builtinInputs{root: root, base: "main", changed: changed, reviewed: reviewed})
	if len(ev.Problems) > 0 || ev.Provenance == nil {
		t.Fatalf("clean chain reported problems %v", ev.Problems)
	}
	if ev.Provenance.Reviewed != reviewed || len(ev.Provenance.Optional) != 0 {
		t.Fatalf("reviewed/optional not carried: %+v", ev.Provenance)
	}
	if len(ev.Provenance.Records) != len(c.want) || len(ev.Provenance.Ends) != len(c.touched) {
		t.Fatalf("evidence carries %d records and %d ends, want %d and %d",
			len(ev.Provenance.Records), len(ev.Provenance.Ends), len(c.want), len(c.touched))
	}
	for p := range c.touched {
		if got, w := ev.Provenance.Ends[p], (core.ChainEnds{Base: blobOf(c.base[p]), Head: blobOf(c.cur[p])}); got != w {
			t.Fatalf("%s chain ends %+v, want %+v", p, got, w)
		}
	}
}

func TestRecordedChainMatchesTheEdits(t *testing.T) {
	gitChecks(t, "20")
	pr := newProvRepo(t, provManifest, nil)
	m, err := ManifestFor(pr.root)
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		pr.reset(t)
		c := newChainModel()
		for i := range rapid.IntRange(1, 3).Draw(t, "edits") {
			c.edit(t, pr, m, fmt.Sprintf("t%d", i))
		}
		c.checkRecords(t, pr.root)
		c.checkEvidence(t, pr.root)
	})
}

func TestRecordRefusesWhatItCannotAttribute(t *testing.T) {
	pr := newProvRepo(t, provManifest, nil)
	m, err := ManifestFor(pr.root)
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		bad := rapid.SampledFrom([]string{"", "..", "a/b", "a b", "../x", strings.Repeat("a", 129)}).Draw(t, "bad id")
		for _, id := range [][2]string{{bad, "t1"}, {"s1", bad}} {
			if err := WritePending(m, id[0], id[1], "p/x.xx"); err == nil {
				t.Fatalf("WritePending accepted session %q tool use %q", id[0], id[1])
			}
			if _, appended, err := Record(m, RecordInput{Session: id[0], ToolUseID: id[1]}); err == nil || appended {
				t.Fatalf("Record accepted session %q tool use %q", id[0], id[1])
			}
		}
	})
	if _, err := os.Stat(filepath.Join(pr.root, core.StateDir, "pending")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an invalid id left pending state behind: %v", err)
	}
	if _, appended, err := Record(m, RecordInput{Session: "s1", ToolUseID: "none"}); !errors.Is(err, ErrNoPending) || appended {
		t.Fatalf("Record without a pending entry: appended %v err %v, want ErrNoPending", appended, err)
	}
	refusesWrongPath(t, pr, m)
	putFile(t, pr.root, core.StateDir+"/pending/s1/t2.json", `{"v":0,"path":"p/x.xx","pre":"nothex"}`)
	if _, _, err := Record(m, RecordInput{Session: "s1", ToolUseID: "t2"}); err == nil || errors.Is(err, ErrNoPending) {
		t.Fatalf("Record of a malformed pending entry: %v", err)
	}
}

func refusesWrongPath(t *testing.T, pr provRepo, m *core.Manifest) {
	t.Helper()
	if err := WritePending(m, "s1", "t1", "p/x.xx"); err != nil {
		t.Fatal(err)
	}
	putFile(t, pr.root, "p/x.xx", "changed\n")
	rel, appended, err := Record(m, RecordInput{Session: "s1", ToolUseID: "t1", Path: "p/other.xx"})
	if err == nil || appended || rel != "p/x.xx" {
		t.Fatalf("Record for a different path: rel %q appended %v err %v", rel, appended, err)
	}
	if _, err := os.Stat(PendingPath(pr.root, "s1", "t1")); err != nil {
		t.Fatalf("a refused Record must keep the pending entry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pr.root, core.ProvenanceDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused Record wrote provenance: %v", err)
	}
}

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func provLine(session, agentID, agentType, post string) string {
	extra := ""
	if agentID != "" {
		extra = fmt.Sprintf(`"agent_id":%q,"agent_type":%q,`, agentID, agentType)
	}
	return fmt.Sprintf(`{"v":0,"session":%q,%s"tool":"Edit","path":"p/x.xx","role":"source","pre":null,"post":%q}`+"\n", session, extra, post)
}

func TestProvenanceEvidenceRejectsTamperedHistory(t *testing.T) {
	gitChecks(t, "30")
	const prov = core.ProvenanceDir
	pr := newProvRepo(t, provManifest, map[string]string{prov + "/s0.jsonl": provLine("s0", "", "", hexA)})
	variants := map[string]struct {
		apply func(t fataler)
		want  string
	}{
		"append": {func(t fataler) {
			putFile(t, pr.root, prov+"/s0.jsonl", provLine("s0", "", "", hexA)+provLine("s0", "", "", hexB))
		}, ""},
		"rewrite": {func(t fataler) { putFile(t, pr.root, prov+"/s0.jsonl", provLine("s0", "", "", hexB)) }, "may only be appended to"},
		"remove":  {func(t fataler) { _ = os.Remove(filepath.Join(pr.root, prov, "s0.jsonl")) }, "may only be appended to"},
		"stray file": {func(t fataler) { putFile(t, pr.root, prov+"/notes.txt", "x\n") },
			"provenance files must be " + prov + "/<session>.jsonl"},
		"other session": {func(t fataler) { putFile(t, pr.root, prov+"/s9.jsonl", provLine("s8", "", "", hexA)) },
			prov + "/s9.jsonl:1: record session \"s8\" does not match the file name"},
		"schema violation": {func(t fataler) { putFile(t, pr.root, prov+"/s7.jsonl", "{\"v\":1}\n") }, prov + "/s7.jsonl"},
		"agent conflict": {func(t fataler) {
			putFile(t, pr.root, prov+"/s1.jsonl", provLine("s1", "a1", core.Implementer, hexA))
			putFile(t, pr.root, prov+"/s2.jsonl", provLine("s2", "a1", core.Verifier, hexA))
		}, "agent_id a1 appears with agent types " + core.Implementer + ", " + core.Verifier},
	}
	names := make([]string, 0, len(variants))
	for n := range variants {
		names = append(names, n)
	}
	slices.Sort(names)
	rapid.Check(t, func(t *rapid.T) {
		pr.reset(t)
		name := rapid.SampledFrom(names).Draw(t, "variant")
		v := variants[name]
		v.apply(t)
		ev := provenanceEvidenceFor(builtinInputs{root: pr.root, base: "main", changed: []core.ChangedFile{{Path: "p/x.xx"}}})
		if v.want == "" {
			if len(ev.Problems) > 0 || ev.Provenance == nil || len(ev.Provenance.Records) != 2 {
				t.Fatalf("%s: problems %v evidence %+v", name, ev.Problems, ev.Provenance)
			}
			return
		}
		if !slices.ContainsFunc(ev.Problems, func(p string) bool { return strings.Contains(p, v.want) }) {
			t.Fatalf("%s: problems %q lack %q", name, ev.Problems, v.want)
		}
	})
}

func TestProtectedEvidenceKeepsOnlyReviewableProtectedFiles(t *testing.T) {
	gitChecks(t, "30")
	provSub := core.ProvenanceDir + "/sub/s.jsonl"
	paths := []string{"assurance.yaml", ".assure/waivers.yaml", ".assure/state/x.json", core.ProvenanceDir + "/s1.jsonl",
		core.ProvenanceDir + "/notes.txt", provSub, "p/x.xx", "docs/guard.md", "docs/deep/a.md"}
	protected := map[string]bool{"assurance.yaml": true, ".assure/waivers.yaml": true, core.ProvenanceDir + "/notes.txt": true,
		provSub: true, "docs/guard.md": true, "docs/deep/a.md": true}
	optional := newProvRepo(t, provManifestOptional, nil)
	plain := newProvRepo(t, provManifest, nil)
	rapid.Check(t, func(t *rapid.T) {
		pr, wantOptional := plain, false
		if rapid.Bool().Draw(t, "optional") {
			pr, wantOptional = optional, true
		}
		m, err := ManifestFor(pr.root)
		if err != nil {
			t.Fatalf("%v", err)
		}
		sel := rapid.SliceOfNDistinct(rapid.SampledFrom(paths), 0, len(paths), rapid.ID[string]).Draw(t, "changed")
		var changed []core.ChangedFile
		var want []string
		for _, p := range sel {
			changed = append(changed, core.ChangedFile{Path: p})
			if protected[p] {
				want = append(want, p)
			}
		}
		reviewed := rapid.Bool().Draw(t, "reviewed")
		ev := protectedEvidenceFor(m, builtinInputs{root: pr.root, base: "main", changed: changed, reviewed: reviewed})
		if ev.Protected == nil {
			t.Fatalf("no protected evidence")
		}
		var got []string
		for _, f := range ev.Protected.Files {
			got = append(got, f.Path)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("protected files %v, want %v", got, want)
		}
		if ev.Protected.Reviewed != reviewed || ev.Protected.Optional[core.Level("B")] != wantOptional {
			t.Fatalf("reviewed/optional not carried: %+v", ev.Protected)
		}
	})
}

func drawReviews(t *rapid.T, head string) []core.Review {
	var revs []core.Review
	for range rapid.IntRange(0, 4).Draw(t, "n") {
		revs = append(revs, core.Review{
			Login:  rapid.SampledFrom([]string{"ann", "Bob", "cy"}).Draw(t, "login"),
			State:  rapid.SampledFrom([]string{"APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING"}).Draw(t, "state"),
			Commit: rapid.SampledFrom([]string{head, hexA}).Draw(t, "commit"),
		})
	}
	return revs
}

func reviewsJSON(docHead string, revs []core.Review, extra string) string {
	var items []string
	for _, r := range revs {
		items = append(items, fmt.Sprintf(`{"login":%q,"state":%q,"commit":%q}`, r.Login, r.State, r.Commit))
	}
	if extra != "" {
		items = append(items, extra)
	}
	return fmt.Sprintf(`{"author":"owner","head":%q,"reviews":[%s]}`, docHead, strings.Join(items, ","))
}

func TestLoadReviewsBindsToHeadAndSchema(t *testing.T) {
	pr := newProvRepo(t, provManifest, nil)
	m, err := ManifestFor(pr.root)
	if err != nil {
		t.Fatal(err)
	}
	head := gitHEAD(t, pr.root)
	dir := t.TempDir()
	file := filepath.Join(dir, "reviews.json")
	rapid.Check(t, func(t *rapid.T) {
		revs := drawReviews(t, head)
		kind := rapid.SampledFrom([]string{"ok", "stale head", "bad state", "missing"}).Draw(t, "kind")
		docHead, extra, target := head, "", file
		switch kind {
		case "stale head":
			docHead = hexB
		case "bad state":
			extra = fmt.Sprintf(`{"login":"x","state":"BOGUS","commit":%q}`, head)
		case "missing":
			target = file + ".absent"
		}
		putFile(t, dir, "reviews.json", reviewsJSON(docHead, revs, extra))
		got, err := LoadReviews(m, target)
		if kind == "ok" {
			if err != nil || got.Author != "owner" || got.Head != head || !slices.Equal(got.Reviews, revs) {
				t.Fatalf("loaded %+v (%v) from %+v", got, err, revs)
			}
			return
		}
		wantErr := map[string]func(error) bool{
			"stale head": func(e error) bool { return e != nil && strings.Contains(e.Error(), "is not HEAD") },
			"bad state": func(e error) bool {
				_, ok := errors.AsType[*core.LoadError](e)
				return ok
			},
			"missing": func(e error) bool { return errors.Is(e, os.ErrNotExist) },
		}[kind]
		if got != nil || !wantErr(err) {
			t.Fatalf("%s: got %+v, err %T %v", kind, got, err, err)
		}
	})
}

func gitChecks(t *testing.T, n string) {
	t.Helper()
	f := flag.Lookup("rapid.checks")
	if f == nil {
		t.Fatal("rapid.checks flag is not registered")
	}
	old := f.Value.String()
	if err := flag.Set("rapid.checks", n); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = flag.Set("rapid.checks", old) })
}
