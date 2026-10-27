package main

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
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
	Untracked    []string            `json:"untracked,omitempty"`
	Environment  *environmentMessage `json:"environment,omitempty"`
}

type unitEnv struct {
	environment
	python string
	tools  map[string]resolved
}

type runContext struct {
	root, top, ref, out, seed string
	sel                       map[string][]string
	units                     map[string]unitEnv
}

type runner func(c runContext) (ev []evidence, env string, err error)

var runners = map[string]runner{
	"VER-TESTS-PASS":          runTests,
	"VER-FAIL-ON-BASE":        runFailOnBase,
	"VER-ROBUST-FUZZ":         runFuzz,
	"VER-MUTATION-CHANGED":    runMutation,
	"VER-COVERAGE-RESOLUTION": runCoverage,
}

const runUsage = "usage: assure-adapter-python run <objective> --changed-from <ref> --out <dir>"

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
		_, _ = fmt.Fprintf(stderr, "assure-adapter-python: objective %q is not implemented\n", obj)
		return 2
	}
	resp, err := execute(fn, obj, *ref, *out)
	if err == nil {
		err = json.NewEncoder(stdout).Encode(resp)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-python: run %s: %v\n", obj, err)
		return 1
	}
	return 0
}

func environmentResponse(msg string) runResponse {
	return runResponse{Protocol: 2, Evidence: []evidence{}, ToolVersions: map[string]string{}, PinnedBy: map[string]string{}, Environment: &environmentMessage{Message: msg}}
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
	c := runContext{root: realPath(wd), ref: ref, out: out, units: map[string]unitEnv{}}
	top, err := adapterkit.ToolOutput(c.root, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return runResponse{}, err
	}
	c.top = realPath(top)
	paths, err := adapterkit.ChangedPaths(c.root, ref)
	if err != nil {
		return runResponse{}, err
	}
	sel, msg, err := selectUnits(c.root, c.top, paths)
	if err != nil {
		return runResponse{}, err
	}
	if msg != "" {
		return environmentResponse(msg), nil
	}
	if len(sel) == 0 {
		unit, msg, err := defaultUnit(c.root, c.top)
		if err != nil {
			return runResponse{}, err
		}
		if msg != "" {
			return environmentResponse(msg), nil
		}
		sel[unit] = []string{}
	}
	c.sel = sel
	resp, env, err := c.resolveUnits(obj)
	if err != nil {
		return runResponse{}, err
	}
	if env != "" {
		return environmentResponse(env), nil
	}
	if c.seed, err = assureSeed(c.root); err != nil {
		return runResponse{}, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return runResponse{}, err
	}
	resp.Evidence, env, err = fn(c)
	if err != nil {
		return runResponse{}, err
	}
	if env != "" {
		return environmentResponse(env), nil
	}
	return resp, nil
}

// defaultUnit names the unit whose tools a run with no changed Python file reports: the manifest
// root's own unit, else the first pyproject.toml under the root.
func defaultUnit(root, top string) (string, string, error) {
	if unit, ok := unitOf(top, root); ok {
		return unit, "", nil
	}
	listed, err := adapterkit.GitPaths(root, "ls-files", "--cached", "--others", "--exclude-standard", "--", "pyproject.toml", "**/pyproject.toml")
	if err != nil {
		return "", "", err
	}
	if len(listed) == 0 {
		return "", "no pyproject.toml in the manifest root or under it; assure-adapter-python needs a uv project to report its tools", nil
	}
	slices.Sort(listed)
	return filepath.Dir(filepath.Join(root, filepath.FromSlash(listed[0]))), "", nil
}

func (c *runContext) resolveUnits(obj string) (runResponse, string, error) {
	resp := runResponse{Protocol: 2, ToolVersions: map[string]string{}, PinnedBy: map[string]string{}}
	for _, unit := range slices.Sorted(maps.Keys(c.sel)) {
		env, msg, err := resolveEnvironment(c.top, unit)
		if err != nil || msg != "" {
			return runResponse{}, msg, err
		}
		rs, msg, err := resolveTools(c.top, env, objectiveTools[obj])
		if err != nil || msg != "" {
			return runResponse{}, msg, err
		}
		u := unitEnv{environment: env, python: filepath.Join(env.venv, "bin", "python"), tools: map[string]resolved{}}
		version, err := adapterkit.ToolOutput(unit, u.python, "--version")
		if err != nil {
			return runResponse{}, "", err
		}
		record(resp.ToolVersions, "python", strings.TrimPrefix(version, "Python "))
		for _, r := range rs {
			u.tools[r.name] = r
			record(resp.ToolVersions, r.name, r.version)
			record(resp.PinnedBy, r.name, r.pinnedBy)
			if r.untracked && !slices.Contains(resp.Untracked, r.name) {
				resp.Untracked = append(resp.Untracked, r.name)
			}
		}
		c.units[unit] = u
	}
	slices.Sort(resp.Untracked)
	return resp, "", nil
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

func pytestArgs(seed, junit string) []string {
	return append([]string{"-m", "pytest", "-p", "no:cacheprovider", "--continue-on-collection-errors",
		"--junitxml=" + junit, "-o", "junit_family=xunit1", "-q"}, seedArgs(seed)...)
}

func (c runContext) pytest(dir, unit, junit, mapRoot, mapTop string, env []string, extra ...string) ([]xmlSuite, []byte, error) {
	args := append(pytestArgs(c.seed, junit), extra...)
	stdout, stderr, code, err := runToolEnv(dir, append([]string{"ASSURE_SEED=" + c.seed}, env...), c.units[unit].python, args...)
	if err != nil {
		return nil, nil, err
	}
	output := append(stdout, stderr...)
	switch code {
	case 0, 1, 2, 5:
	default:
		return nil, nil, fmt.Errorf("pytest in %s exited %d: %s", relSlash(c.top, unit), code, capText(output))
	}
	if code == 5 {
		return []xmlSuite{}, output, nil
	}
	r, err := readPytestReport(junit)
	if err != nil {
		return nil, nil, fmt.Errorf("pytest in %s exited %d without a readable report (%w): %s", relSlash(c.top, unit), code, err, capText(output))
	}
	suites := toJUnit(mapRoot, mapTop, dir, r)
	if code != 0 && !hasFailure(suites) {
		return nil, nil, fmt.Errorf("pytest in %s exited %d with no failing test or collection error in its report: %s", relSlash(c.top, unit), code, capText(output))
	}
	return suites, output, nil
}

func runTests(c runContext) ([]evidence, string, error) {
	tmp, err := os.MkdirTemp("", "assure-pytest-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	suites := []xmlSuite{}
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		if len(c.sel[unit]) == 0 {
			continue
		}
		s, _, err := c.pytest(unit, unit, filepath.Join(tmp, fmt.Sprintf("pytest-%d.xml", i)), c.root, c.top, nil)
		if err != nil {
			return nil, "", err
		}
		suites = append(suites, s...)
	}
	if err := writeJUnit(filepath.Join(c.out, "junit.xml"), suites); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "test.junit", Path: "junit.xml"}}, "", nil
}
