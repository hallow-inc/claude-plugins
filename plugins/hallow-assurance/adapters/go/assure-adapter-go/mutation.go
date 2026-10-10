package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const timeoutCoefficient = 10

var strykerStatus = map[string]string{
	"KILLED":      "Killed",
	"LIVED":       "Survived",
	"NOT COVERED": "NoCoverage",
	"TIMED OUT":   "Timeout",
	"NOT VIABLE":  "CompileError",
	"SKIPPED":     "Ignored",
}

type gremlinsOutput struct {
	Files []struct {
		FileName  string `json:"file_name"`
		Mutations []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		} `json:"mutations"`
	} `json:"files"`
}

type strykerPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type strykerMutant struct {
	ID          string `json:"id"`
	MutatorName string `json:"mutatorName"`
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

func (r strykerReport) addGremlins(data []byte, modRel string, source func(repoPath string) (string, error)) error {
	var g gremlinsOutput
	if err := json.Unmarshal(data, &g); err != nil {
		return fmt.Errorf("gremlins output: %w", err)
	}
	for _, f := range g.Files {
		p := path.Clean(path.Join(modRel, filepath.ToSlash(f.FileName)))
		sf := r.Files[p]
		if sf == nil {
			src, err := source(p)
			if err != nil {
				return err
			}
			sf = &strykerFile{Language: "go", Source: src, Mutants: []strykerMutant{}}
			r.Files[p] = sf
		}
		for _, m := range f.Mutations {
			status, ok := strykerStatus[m.Status]
			if !ok {
				return fmt.Errorf("gremlins output: %s:%d:%d %s has unknown status %q", p, m.Line, m.Column, m.Type, m.Status)
			}
			sm := strykerMutant{ID: fmt.Sprintf("%s:%d:%d:%s", p, m.Line, m.Column, m.Type), MutatorName: m.Type, Status: status}
			sm.Location.Start = strykerPosition{Line: m.Line, Column: m.Column}
			sm.Location.End = sm.Location.Start
			sf.Mutants = append(sf.Mutants, sm)
		}
	}
	return nil
}

func rapidSeed(root string) (string, error) {
	sha, err := toolOutput(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if len(sha) < 16 {
		return "", fmt.Errorf("git rev-parse HEAD: unexpected output %q", sha)
	}
	n, err := strconv.ParseUint(sha[:16], 16, 64)
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strconv.FormatUint(max(n, 1), 10), nil
}

func gremlinsVersion() (string, error) {
	bin, err := exec.LookPath("gremlins")
	if err != nil {
		return "", errors.New("gremlins not found on PATH")
	}
	out, err := toolOutput(".", "go", "version", "-m", bin)
	if err != nil {
		return "", err
	}
	for l := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "mod" && f[1] == "github.com/go-gremlins/gremlins" {
			return f[2], nil
		}
	}
	return "", fmt.Errorf("go version -m %s: no github.com/go-gremlins/gremlins module line", bin)
}

func runMutation(root, ref, out string, sel map[string][]string, _ map[string]string) ([]evidence, error) {
	seed, err := rapidSeed(root)
	if err != nil {
		return nil, err
	}
	rep := newStrykerReport()
	source := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		return string(b), err
	}
	for _, mod := range modules(sel) {
		data, err := unleash(filepath.Join(root, mod), ref, out, seed)
		if err != nil {
			return nil, err
		}
		if err := rep.addGremlins(data, mod, source); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "mutation.json"), data, 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "mutation.report", Path: "mutation.json"}}, nil
}

func unleash(dir, ref, out, seed string) ([]byte, error) {
	f, err := os.CreateTemp(out, "gremlins-*.json")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()
	cmd := exec.CommandContext(context.Background(), "gremlins", "unleash", "--diff", ref, "--output", name,
		"--timeout-coefficient", strconv.Itoa(timeoutCoefficient), ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "RAPID_SEED="+seed,
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=diff.relative", "GIT_CONFIG_VALUE_0=true")
	log, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 10 || exit.ExitCode() == 11) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("gremlins unleash in %s: %w\n%s", dir, err, capText(log))
	}
	return os.ReadFile(name)
}
