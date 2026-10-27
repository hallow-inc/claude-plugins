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

type runContext struct {
	root, top, ref, out string
	sel                 map[string][]string
	tools               map[string]map[string]resolved
}

type runner func(c runContext) (ev []evidence, env string, err error)

var runners = map[string]runner{
	"VER-TESTS-PASS":          runTests,
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
	c := runContext{root: realPath(wd), ref: ref, out: out, tools: map[string]map[string]resolved{}}
	if !onPath("node") {
		return environment("node is not on PATH; install the Node version the repository's CI uses"), nil
	}
	nodeVersion, err := adapterkit.ToolOutput(c.root, "node", "--version")
	if err != nil {
		return runResponse{}, err
	}
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
		return environment(msg), nil
	}
	c.sel = sel
	resp, env, err := c.resolveUnits(obj)
	if err != nil {
		return runResponse{}, err
	}
	if env != "" {
		return environment(env), nil
	}
	resp.ToolVersions["node"] = nodeVersion
	if err := os.MkdirAll(out, 0o755); err != nil {
		return runResponse{}, err
	}
	resp.Evidence, env, err = fn(c)
	if err != nil {
		return runResponse{}, err
	}
	if env != "" {
		return environment(env), nil
	}
	return resp, nil
}

func (c *runContext) resolveUnits(obj string) (runResponse, string, error) {
	resp := runResponse{Protocol: 2, ToolVersions: map[string]string{}, PinnedBy: map[string]string{}}
	for _, unit := range slices.Sorted(maps.Keys(c.sel)) {
		rs, env, err := resolveTools(c.top, unit, objectiveTools[obj])
		if err != nil || env != "" {
			return runResponse{}, env, err
		}
		c.tools[unit] = map[string]resolved{}
		for _, r := range rs {
			c.tools[unit][r.name] = r
			record(resp.ToolVersions, r.name, r.version)
			record(resp.PinnedBy, r.name, r.pinnedBy)
			if r.untracked && !slices.Contains(resp.Untracked, r.name) {
				resp.Untracked = append(resp.Untracked, r.name)
			}
		}
	}
	slices.Sort(resp.Untracked)
	return resp, "", nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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
	suites := adapterkit.XMLSuites{Suites: []adapterkit.XMLSuite{}}
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
