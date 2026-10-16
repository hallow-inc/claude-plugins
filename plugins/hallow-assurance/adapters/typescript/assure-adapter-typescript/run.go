package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type evidence struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type runResponse struct {
	Protocol     int                 `json:"protocol"`
	Evidence     []evidence          `json:"evidence"`
	ToolVersions map[string]string   `json:"tool_versions"`
	PinnedBy     map[string]string   `json:"pinned_by"`
	Environment  *environmentMessage `json:"environment,omitempty"`
}

type runContext struct {
	root, top, ref, out string
	sel                 map[string][]string
	tools               map[string]map[string]resolved
}

type runner func(c runContext) (ev []evidence, env string, err error)

var runners = map[string]runner{
	"VER-TESTS-PASS":          runTests,
	"CODE-ZERO-WARNINGS":      runLint,
	"CODE-CHECK-RETURNS":      checkReturnsPack.run,
	"CODE-COMPLEXITY":         complexityPack.run,
	"CODE-NO-UNSAFE":          noUnsafePack.run,
	"CODE-RESOURCE-BOUNDS":    resourceBoundsPack.run,
	"VER-MUTATION-CHANGED":    runMutation,
	"VER-FAIL-ON-BASE":        runFailOnBase,
	"VER-ROBUST-FUZZ":         runFuzz,
	"VER-COVERAGE-RESOLUTION": runCoverage,
}

const runUsage = "usage: assure-adapter-typescript run <objective> --changed-from <ref> --out <dir>"

func runObjective(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, runUsage)
		return 2
	}
	obj := args[0]
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ref := fs.String("changed-from", "", "git ref to diff against")
	out := fs.String("out", "", "directory for evidence files")
	if err := fs.Parse(args[1:]); err != nil || *ref == "" || *out == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, runUsage)
		return 2
	}
	fn, ok := runners[obj]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: objective %q is not implemented\n", obj)
		return 2
	}
	resp, err := execute(fn, obj, *ref, *out)
	if err == nil {
		err = json.NewEncoder(stdout).Encode(resp)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: run %s: %v\n", obj, err)
		return 1
	}
	return 0
}

func environment(msg string) runResponse {
	return runResponse{Protocol: 1, Evidence: []evidence{}, ToolVersions: map[string]string{}, PinnedBy: map[string]string{}, Environment: &environmentMessage{Message: msg}}
}

func record(m map[string]string, k, v string) {
	old, ok := m[k]
	if !ok {
		m[k] = v
	} else if !slices.Contains(strings.Split(old, ", "), v) {
		m[k] = old + ", " + v
	}
}

func execute(fn runner, obj, ref, out string) (runResponse, error) {
	wd, err := os.Getwd()
	if err != nil {
		return runResponse{}, err
	}
	c := runContext{root: realPath(wd), ref: ref, out: out, tools: map[string]map[string]resolved{}}
	if !onPath("node") {
		return environment("node is not on PATH; install the Node version the repository's CI uses"), nil
	}
	nodeVersion, err := toolOutput(c.root, "node", "--version")
	if err != nil {
		return runResponse{}, err
	}
	top, err := gitOutput(c.root, "rev-parse", "--show-toplevel")
	if err != nil {
		return runResponse{}, err
	}
	c.top = realPath(top)
	paths, err := changedPaths(c.root, ref)
	if err != nil {
		return runResponse{}, err
	}
	sel, msg, err := selectUnits(c.root, c.top, paths)
	if err != nil {
		return runResponse{}, err
	}
	if msg != "" {
		return environment(msg), nil
	}
	c.sel = sel
	versions, pinnedBy := map[string]string{}, map[string]string{}
	for _, unit := range slices.Sorted(maps.Keys(sel)) {
		rs, env, err := resolveTools(c.top, unit, objectiveTools[obj])
		if err != nil {
			return runResponse{}, err
		}
		if env != "" {
			return environment(env), nil
		}
		c.tools[unit] = map[string]resolved{}
		for _, r := range rs {
			c.tools[unit][r.name] = r
			record(versions, r.name, r.version)
			record(pinnedBy, r.name, r.pinnedBy)
		}
	}
	versions["node"] = nodeVersion
	if err := os.MkdirAll(out, 0o755); err != nil {
		return runResponse{}, err
	}
	ev, env, err := fn(c)
	if err != nil {
		return runResponse{}, err
	}
	if env != "" {
		return environment(env), nil
	}
	return runResponse{Protocol: 1, Evidence: ev, ToolVersions: versions, PinnedBy: pinnedBy}, nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func toolOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}

func changedPaths(root, ref string) ([]string, error) {
	diff, err := gitPaths(root, "diff", "--name-only", "--relative", ref, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := gitPaths(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return append(diff, untracked...), nil
}

func runTool(dir, name string, args ...string) (stdout, stderr []byte, code int, err error) {
	return runToolEnv(dir, nil, name, args...)
}

func runToolEnv(dir string, env []string, name string, args ...string) (stdout, stderr []byte, code int, err error) {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return o.Bytes(), e.Bytes(), ee.ExitCode(), nil
	}
	return o.Bytes(), e.Bytes(), 0, err
}

func absFiles(root string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Join(root, filepath.FromSlash(f)))
	}
	return out
}

func runTests(c runContext) ([]evidence, string, error) {
	tmp, err := os.MkdirTemp("", "assure-vitest-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	seed, err := assureSeed(c.root)
	if err != nil {
		return nil, "", err
	}
	suites := xmlSuites{Suites: []xmlSuite{}}
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		if len(c.sel[unit]) == 0 {
			continue
		}
		report := filepath.Join(tmp, fmt.Sprintf("vitest-%d.json", i))
		args := append([]string{"related", "--run", "--passWithNoTests", "--sequence.shuffle", "--reporter=json", "--outputFile=" + report}, absFiles(c.root, c.sel[unit])...)
		stdout, stderr, code, err := runToolEnv(unit, []string{"ASSURE_SEED=" + seed}, c.tools[unit]["vitest"].bin, args...)
		if err != nil {
			return nil, "", err
		}
		suites.Suites = append(suites.Suites, vitestSuites(c.root, relSlash(c.top, unit), report, append(stdout, stderr...), code)...)
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(c.out, "junit.xml"), append([]byte(xml.Header), data...), 0o644); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "test.junit", Path: "junit.xml"}}, "", nil
}

var eslintConfigs = globsFor("eslint.config.%s", configExts)

func hasESLintConfig(top, unit string) bool {
	for _, name := range eslintConfigs {
		if _, ok := findUp(top, unit, name); ok {
			return true
		}
	}
	return false
}

func lintEnvironment(top string, units []string) string {
	for _, unit := range units {
		if !exists(filepath.Join(unit, "tsconfig.json")) {
			return fmt.Sprintf("%s is missing; CODE-ZERO-WARNINGS runs tsc --noEmit on each unit's tsconfig.json", relSlash(top, filepath.Join(unit, "tsconfig.json")))
		}
		if !hasESLintConfig(top, unit) {
			return fmt.Sprintf("%s has no ESLint flat configuration (eslint.config.<ext>) in it or any parent up to the git top-level; CODE-ZERO-WARNINGS runs the repository's ESLint configuration", relSlash(top, unit))
		}
	}
	return ""
}

func runLint(c runContext) ([]evidence, string, error) {
	units := slices.Sorted(maps.Keys(c.sel))
	if env := lintEnvironment(c.top, units); env != "" {
		return nil, env, nil
	}
	eslint, tsc := newRun("eslint"), newRun("tsc")
	for _, unit := range units {
		name := relSlash(c.top, unit)
		if files := c.sel[unit]; len(files) > 0 {
			args := append([]string{"--format", "json", "--no-warn-ignored"}, absFiles(c.root, files)...)
			stdout, stderr, code, err := runTool(unit, c.tools[unit]["eslint"].bin, args...)
			if err != nil {
				return nil, "", err
			}
			eslint.Results = append(eslint.Results, eslintResults(c.root, name, stdout, stderr, code == 0 || code == 1)...)
		}
		stdout, stderr, code, err := runTool(unit, c.tools[unit]["typescript"].bin, "--noEmit", "-p", filepath.Join(unit, "tsconfig.json"), "--pretty", "false")
		if err != nil {
			return nil, "", err
		}
		tsc.Results = append(tsc.Results, tscResults(c.root, unit, name, append(stdout, stderr...), code != 0)...)
	}
	ev, err := writeSARIF(c.out, eslint, tsc)
	return ev, "", err
}

func writeSARIF(out string, runs ...sarifRun) ([]evidence, error) {
	data, err := json.Marshal(sarifLog{Version: "2.1.0", Runs: runs})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "lint.sarif"), data, 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "lint.sarif", Path: "lint.sarif"}}, nil
}
