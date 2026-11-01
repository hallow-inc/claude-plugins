package app

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	auditFindingsFile   = "findings.json"
	auditKindMutant     = "surviving-mutant"
	auditKindUncovered  = "uncovered"
	auditKindFuzz       = "missing-fuzz"
	auditKindRed        = "tests-failing"
	auditUnscoredTarget = 100
)

type AuditFindings struct {
	Version      int                          `json:"version"`
	Commit       string                       `json:"commit"`
	Dirty        bool                         `json:"dirty"`
	Scope        []string                     `json:"scope"`
	ToolVersions map[string]map[string]string `json:"tool_versions"`
	Findings     []AuditFinding               `json:"findings"`
	Scores       []AuditScore                 `json:"scores"`
	Unaudited    []AuditUnaudited             `json:"unaudited"`
}

type AuditFinding struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Language string     `json:"language"`
	Path     string     `json:"path,omitempty"`
	Line     int        `json:"line,omitempty"`
	Level    core.Level `json:"level,omitempty"`
	Detail   string     `json:"detail"`
}

type AuditScore struct {
	Language  string     `json:"language"`
	Level     core.Level `json:"level"`
	Detected  int        `json:"detected"`
	Total     int        `json:"total"`
	Percent   float64    `json:"percent"`
	Threshold float64    `json:"threshold"`
	Advisory  bool       `json:"advisory"`
}

type AuditUnaudited struct {
	Language string `json:"language"`
	Message  string `json:"message"`
}

func (f AuditFindings) Marshal() ([]byte, error) {
	if f.ToolVersions == nil {
		f.ToolVersions = map[string]map[string]string{}
	}
	f.Findings = orEmpty(f.Findings)
	f.Scores = orEmpty(f.Scores)
	f.Unaudited = orEmpty(f.Unaudited)
	data, _ := json.MarshalIndent(f, "", "  ")
	data = append(data, '\n')
	vs, err := schemas.Validate(schemas.AuditFindings, data)
	if err == nil && len(vs) > 0 {
		msgs := make([]string, len(vs))
		for i, v := range vs {
			msgs[i] = v.String()
		}
		err = errors.New(strings.Join(msgs, "; "))
	}
	if err != nil {
		return nil, fmt.Errorf("findings violate %s.schema.json: %w", schemas.AuditFindings, err)
	}
	return data, nil
}

type draftFinding struct {
	AuditFinding
	content string
}

func finalizeFindings(ds []draftFinding) []AuditFinding {
	slices.SortFunc(ds, func(a, b draftFinding) int {
		return cmp.Or(cmp.Compare(a.Language, b.Language), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.content, b.content))
	})
	seen := map[string]int{}
	out := make([]AuditFinding, len(ds))
	for i, d := range ds {
		sum := sha256.Sum256([]byte(d.Kind + "\x00" + d.Path + "\x00" + d.content))
		id := hex.EncodeToString(sum[:8])
		seen[id]++
		d.ID = id
		if n := seen[id]; n > 1 {
			d.ID += "-" + strconv.Itoa(n)
		}
		out[i] = d.AuditFinding
	}
	slices.SortFunc(out, func(a, b AuditFinding) int {
		return cmp.Or(cmp.Compare(a.Language, b.Language), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	return out
}

type langAudit struct {
	mu       sync.Mutex
	root     string
	lang     string
	outcomes []AuditOutcome
	findings []draftFinding
	scores   map[core.Level]*AuditScore
	reasons  []string
	versions map[string]string
	sources  map[string][]string
}

func newLangAudit(root, lang string) *langAudit {
	return &langAudit{root: root, lang: lang, scores: map[core.Level]*AuditScore{}, versions: map[string]string{}, sources: map[string][]string{}}
}

func runProblem(ev core.Evidence, run adapterproto.Run) string {
	if run.Environment != nil {
		return run.Environment.Message
	}
	if len(ev.Errs) > 0 {
		return ev.Errs[0].Error()
	}
	return strings.Join(ev.Problems, "; ")
}

func (la *langAudit) record(j job, ev core.Evidence, run adapterproto.Run) {
	la.mu.Lock()
	defer la.mu.Unlock()
	maps.Copy(la.versions, run.ToolVersions)
	if reason := runProblem(ev, run); reason != "" {
		la.reasons = append(la.reasons, reason)
		return
	}
	la.outcomes = append(la.outcomes, AuditOutcome{Lang: la.lang, Objective: j.obj.ID, Status: auditRan})
	la.testsFailing(ev.Failing)
	la.mutants(j.obj, ev.Mutants)
	if ev.Coverage != nil {
		la.coverage(j, *ev.Coverage)
	}
	if ev.Fuzz != nil {
		la.fuzz(j, *ev.Fuzz)
	}
}

func (la *langAudit) add(f AuditFinding, content string) {
	f.Language = la.lang
	la.findings = append(la.findings, draftFinding{AuditFinding: f, content: content})
}

func (la *langAudit) text(path string, line int) string {
	lines, ok := la.sources[path]
	if !ok {
		data, _ := os.ReadFile(filepath.Join(la.root, filepath.FromSlash(path)))
		lines = strings.Split(string(data), "\n")
		la.sources[path] = lines
	}
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

func (la *langAudit) testsFailing(fs []core.TestFailure) {
	if len(fs) == 0 {
		return
	}
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.Name
	}
	la.add(AuditFinding{Kind: auditKindRed, Detail: "failing tests: " + strings.Join(names, ", ")}, la.lang)
}

func (la *langAudit) mutants(o core.Objective, ms []core.Mutant) {
	for _, m := range ms {
		scored, killed := core.ScoredMutant(m.Status)
		if !scored || o.Levels[m.Level] == "" {
			continue
		}
		s := la.score(o, m.Level)
		s.Total++
		if killed {
			s.Detected++
			continue
		}
		la.add(AuditFinding{Kind: auditKindMutant, Path: m.Path, Line: m.Line, Level: m.Level, Detail: m.Mutator + " " + m.Status},
			la.text(m.Path, m.Line)+"\x00"+m.Mutator)
	}
}

func (la *langAudit) score(o core.Objective, l core.Level) *AuditScore {
	s, ok := la.scores[l]
	if !ok {
		th, has := o.Threshold[l]
		if !has {
			th = auditUnscoredTarget
		}
		s = &AuditScore{Language: la.lang, Level: l, Threshold: th, Advisory: true}
		la.scores[l] = s
	}
	return s
}

func (la *langAudit) coverage(j job, c core.Coverage) {
	files := slices.Clone(j.files)
	for i, f := range files {
		if f.Lines == nil {
			files[i].Lines = recordedLines(c, f.Path)
		}
	}
	us, _ := core.UncoveredLines(j.obj, files, c)
	for _, u := range us {
		if u.Line == 0 {
			la.reasons = append(la.reasons, u.Text)
			continue
		}
		la.add(AuditFinding{Kind: auditKindUncovered, Path: u.Path, Line: u.Line, Level: u.Level, Detail: u.Text},
			u.Function+"\x00"+la.text(u.Path, u.Line))
	}
}

func recordedLines(c core.Coverage, path string) []int {
	var lines []int
	for _, f := range c.Files {
		if f.Path == path {
			lines = append(lines, slices.Collect(maps.Keys(f.Lines))...)
		}
	}
	slices.Sort(lines)
	return slices.Compact(lines)
}

func (la *langAudit) fuzz(j job, run core.FuzzRun) {
	for _, f := range core.FuzzFindings(j.obj, j.files, run) {
		la.add(AuditFinding{Kind: auditKindFuzz, Path: f.Path, Level: f.Level, Detail: f.Text}, f.Text)
	}
}

func (la *langAudit) sortedScores() []AuditScore {
	var out []AuditScore
	for _, l := range slices.Sorted(maps.Keys(la.scores)) {
		s := *la.scores[l]
		s.Percent = 100 * float64(s.Detected) / float64(s.Total)
		out = append(out, s)
	}
	return out
}
