package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type lineRange struct{ start, end int }

var hunkHeader = regexp.MustCompile(`(?m)^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

func hunkRanges(diff []byte) ([]lineRange, error) {
	var out []lineRange
	for _, m := range hunkHeader.FindAllSubmatch(diff, -1) {
		start, err := strconv.Atoi(string(m[1]))
		if err != nil {
			return nil, fmt.Errorf("git diff hunk %q: %w", m[0], err)
		}
		count := 1
		if len(m[2]) > 0 {
			if count, err = strconv.Atoi(string(m[2])); err != nil {
				return nil, fmt.Errorf("git diff hunk %q: %w", m[0], err)
			}
		}
		if count > 0 {
			out = append(out, lineRange{start, start + count - 1})
		}
	}
	return out, nil
}

func mutateEntries(c runContext, unit string) ([]string, error) {
	var out []string
	for _, f := range c.sel[unit] {
		role, _, err := classify(f)
		if err != nil {
			return nil, err
		}
		if role != "source" {
			continue
		}
		diff, err := gitOutput(c.root, "diff", "-U0", "--no-color", "--no-ext-diff", "--no-textconv", c.ref, "--", f)
		if err != nil {
			return nil, err
		}
		ranges, err := hunkRanges([]byte(diff))
		if err != nil {
			return nil, err
		}
		rel := relSlash(unit, filepath.Join(c.root, filepath.FromSlash(f)))
		for _, r := range ranges {
			out = append(out, fmt.Sprintf("%s:%d-%d", rel, r.start, r.end))
		}
	}
	return out, nil
}

type strykerPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type strykerMutant struct {
	ID          string `json:"id"`
	MutatorName string `json:"mutatorName"`
	Replacement string `json:"replacement,omitempty"`
	Location    struct {
		Start strykerPosition `json:"start"`
		End   strykerPosition `json:"end"`
	} `json:"location"`
	Status string `json:"status"`
}

type strykerFile struct {
	Language string          `json:"language"`
	Source   string          `json:"source"`
	Mutants  []strykerMutant `json:"mutants"`
}

type strykerReport struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Thresholds    map[string]int          `json:"thresholds"`
	Files         map[string]*strykerFile `json:"files"`
}

func newStrykerReport() strykerReport {
	return strykerReport{SchemaVersion: "1", Thresholds: map[string]int{"high": 80, "low": 60}, Files: map[string]*strykerFile{}}
}

func mutantID(file string, m strykerMutant) string {
	s, e := m.Location.Start, m.Location.End
	return fmt.Sprintf("%s:%d:%d-%d:%d:%s:%s", file, s.Line, s.Column, e.Line, e.Column, m.MutatorName, m.Replacement)
}

func (r strykerReport) addStryker(data []byte, rel func(string) (string, error)) error {
	var in struct {
		Files map[string]strykerFile `json:"files"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("stryker report: %w", err)
	}
	ids := map[string]bool{}
	for _, f := range r.Files {
		for _, m := range f.Mutants {
			ids[m.ID] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(in.Files)) {
		p, err := rel(name)
		if err != nil {
			return err
		}
		if r.Files[p] != nil {
			return fmt.Errorf("stryker report: %s reported by two units", p)
		}
		f := in.Files[name]
		out := &strykerFile{Language: "typescript", Source: f.Source, Mutants: []strykerMutant{}}
		for _, m := range f.Mutants {
			m.ID = mutantID(p, m)
			if m.Status == "Pending" {
				return fmt.Errorf("stryker report: mutant %s is Pending; Stryker did not finish it", m.ID)
			}
			if ids[m.ID] {
				return fmt.Errorf("stryker report: two mutants have id %s", m.ID)
			}
			ids[m.ID] = true
			out.Mutants = append(out.Mutants, m)
		}
		r.Files[p] = out
	}
	return nil
}

func (r strykerReport) encode() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	vs, err := schemas.Validate(schemas.MutationReport, data)
	if err != nil {
		return nil, fmt.Errorf("mutation report: %w", err)
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("mutation report: %s", vs[0])
	}
	return data, nil
}

func strykerConfig(dir string, mutate []string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"testRunner":   "vitest",
		"plugins":      []string{"@stryker-mutator/vitest-runner"},
		"reporters":    []string{"json"},
		"jsonReporter": map[string]string{"fileName": filepath.Join(dir, "report.json")},
		"thresholds":   map[string]any{"high": 80, "low": 60, "break": nil},
		"tempDirName":  filepath.Join(dir, "sandbox"),
		"mutate":       mutate,
	})
}

func stryker(c runContext, unit string, mutate []string, seed string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "assure-stryker-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// vitest related finds no tests when the sandbox path runs through a symlink (macOS /var).
	cfg, err := strykerConfig(realPath(dir), mutate)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(dir, "stryker.config.json")
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(context.Background(), c.tools[unit]["@stryker-mutator/core"].bin, "run", cfgPath)
	cmd.Dir = unit
	cmd.Env = append(os.Environ(), "ASSURE_SEED="+seed)
	if log, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stryker run in %s: %w\n%s", relSlash(c.top, unit), err, capText(log))
	}
	return os.ReadFile(filepath.Join(dir, "report.json"))
}

func runMutation(c runContext) ([]evidence, string, error) {
	seed, err := assureSeed(c.root)
	if err != nil {
		return nil, "", err
	}
	rep := newStrykerReport()
	for _, unit := range slices.Sorted(maps.Keys(c.sel)) {
		mutate, err := mutateEntries(c, unit)
		if err != nil {
			return nil, "", err
		}
		if len(mutate) == 0 {
			continue
		}
		data, err := stryker(c, unit, mutate, seed)
		if err != nil {
			return nil, "", err
		}
		rel := func(name string) (string, error) {
			p := relSlash(c.root, filepath.Join(unit, filepath.FromSlash(name)))
			if p == ".." || strings.HasPrefix(p, "../") || filepath.IsAbs(p) {
				return "", fmt.Errorf("stryker report: %s is outside %s", name, c.root)
			}
			return p, nil
		}
		if err := rep.addStryker(data, rel); err != nil {
			return nil, "", err
		}
	}
	data, err := rep.encode()
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(c.out, "mutation.json"), data, 0o644); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "mutation.report", Path: "mutation.json"}}, "", nil
}
