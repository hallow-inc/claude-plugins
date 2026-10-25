package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed helper.py
var helperScript string

type pyTest struct {
	Class string `json:"class"`
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

func (t pyTest) id(rel string) string {
	if t.Class == "" {
		return rel + "::" + t.Name
	}
	return rel + "::" + t.Class + "::" + t.Name
}

type pyFunc struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type pyFile struct {
	Tests     []pyTest `json:"tests"`
	Functions []pyFunc `json:"functions"`
	Error     string   `json:"error"`
}

type pyConfig struct {
	Keys  []string `json:"keys"`
	Error string   `json:"error"`
}

func runHelper(python, dir, mode string, paths []string, out any) error {
	if len(paths) == 0 {
		return nil
	}
	cmd := exec.CommandContext(context.Background(), python, append([]string{"-I", "-c", helperScript, mode}, paths...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s helper: %w: %s", python, err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("%s helper: %w", python, err)
	}
	return nil
}

func describeFiles(python, dir string, paths []string) (map[string]pyFile, error) {
	out := map[string]pyFile{}
	if err := runHelper(python, dir, "files", paths, &out); err != nil {
		return nil, err
	}
	for _, p := range paths {
		if _, ok := out[p]; !ok {
			return nil, fmt.Errorf("helper gave no result for %s", p)
		}
	}
	return out, nil
}

var pytestConfigFiles = []string{"pyproject.toml", "pytest.ini", "tox.ini", "setup.cfg"}

func discoveryRefusal(python, top, unit string) (string, error) {
	var paths []string
	for dir := unit; ; dir = filepath.Dir(dir) {
		for _, n := range pytestConfigFiles {
			if p := filepath.Join(dir, n); exists(p) {
				paths = append(paths, p)
			}
		}
		if dir == top || filepath.Dir(dir) == dir {
			break
		}
	}
	out := map[string]pyConfig{}
	if err := runHelper(python, unit, "config", paths, &out); err != nil {
		return "", err
	}
	var problems []string
	for _, p := range paths {
		c, ok := out[p]
		switch {
		case !ok:
			return "", fmt.Errorf("helper gave no result for %s", p)
		case c.Error != "":
			problems = append(problems, fmt.Sprintf("%s could not be read for pytest discovery settings: %s", relSlash(top, p), c.Error))
		case len(c.Keys) > 0:
			problems = append(problems, fmt.Sprintf("%s sets %s; assure-adapter-python supports only pytest's default test discovery, so it could not tell which functions are tests", relSlash(top, p), strings.Join(c.Keys, ", ")))
		}
	}
	return strings.Join(problems, "\n"), nil
}
