package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type evidence struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type runResponse struct {
	Protocol     int               `json:"protocol"`
	Evidence     []evidence        `json:"evidence"`
	ToolVersions map[string]string `json:"tool_versions"`
}

type runner func(root, ref, out string, sel map[string][]string, versions map[string]string) ([]evidence, error)

var runners = map[string]runner{
	"VER-TESTS-PASS":          runTests,
	"CODE-ZERO-WARNINGS":      runLint,
	"CODE-CHECK-RETURNS":      checkReturnsPack.run,
	"CODE-RESOURCE-BOUNDS":    resourceBoundsPack.run,
	"CODE-COMPLEXITY":         runComplexity,
	"CODE-NO-UNSAFE":          runNoUnsafe,
	"VER-MUTATION-CHANGED":    runMutation,
	"VER-FAIL-ON-BASE":        runFailOnBase,
	"VER-ROBUST-FUZZ":         runFuzz,
	"VER-TEST-BUDGET":         runBudget,
	"VER-COVERAGE-RESOLUTION": runCoverage,
}

func runObjective(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-go run <objective> --changed-from <ref> --out <dir>")
		return 2
	}
	obj := args[0]
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ref := fs.String("changed-from", "", "git ref to diff against")
	out := fs.String("out", "", "directory for evidence files")
	if err := fs.Parse(args[1:]); err != nil || *ref == "" || *out == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-go run <objective> --changed-from <ref> --out <dir>")
		return 2
	}
	fn, ok := runners[obj]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-go: objective %q is not implemented\n", obj)
		return 2
	}
	resp, err := execute(fn, obj, *ref, *out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-go: run %s: %v\n", obj, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(resp); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-go: %v\n", err)
		return 1
	}
	return 0
}

func execute(fn runner, obj, ref, out string) (runResponse, error) {
	root, err := os.Getwd()
	if err != nil {
		return runResponse{}, err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	versions := map[string]string{}
	v, err := toolOutput(root, "go", "env", "GOVERSION")
	if err != nil {
		return runResponse{}, err
	}
	versions["go"] = v
	paths, err := changedPaths(root, ref)
	if err != nil {
		return runResponse{}, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return runResponse{}, err
	}
	ev, err := fn(root, ref, out, selectPackages(root, paths), versions)
	if err != nil {
		return runResponse{}, err
	}
	return runResponse{Protocol: 0, Evidence: ev, ToolVersions: versions}, nil
}

func toolOutput(dir, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s not found on PATH", name)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}

func gitLines(dir string, args ...string) ([]string, error) {
	out, err := toolOutput(dir, "git", args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for l := range strings.SplitSeq(out, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func changedPaths(root, ref string) ([]string, error) {
	diff, err := gitLines(root, "diff", "--name-only", "--relative", ref, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := gitLines(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return append(diff, untracked...), nil
}

func ignoredByGo(p string) bool {
	for seg := range strings.SplitSeq(path.Dir(p), "/") {
		if seg == "testdata" || seg == "vendor" || (seg != "." && (strings.HasPrefix(seg, "_") || strings.HasPrefix(seg, "."))) {
			return true
		}
	}
	return false
}

func moduleOf(root, dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(root, dir, "go.mod")); err == nil {
			return dir, true
		}
		if dir == "." {
			return "", false
		}
		dir = path.Dir(dir)
	}
}

func hasGoFiles(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return len(m) > 0
}

func selectPackages(root string, paths []string) map[string][]string {
	whole := map[string]bool{}
	pkgs := map[string]map[string]bool{}
	for _, p := range paths {
		dir := path.Dir(p)
		switch {
		case configFiles[path.Base(p)]:
			if mod, ok := moduleOf(root, dir); ok {
				whole[mod] = true
			}
		case strings.HasSuffix(p, ".go") && !ignoredByGo(p) && hasGoFiles(filepath.Join(root, dir)):
			if mod, ok := moduleOf(root, dir); ok {
				if pkgs[mod] == nil {
					pkgs[mod] = map[string]bool{}
				}
				rel := strings.TrimPrefix(strings.TrimPrefix(dir, mod), "/")
				pkgs[mod]["./"+path.Clean(rel)] = true
			}
		}
	}
	sel := map[string][]string{}
	for mod := range whole {
		sel[mod] = []string{"./..."}
	}
	for mod, set := range pkgs {
		if whole[mod] {
			continue
		}
		for p := range set {
			sel[mod] = append(sel[mod], p)
		}
		sort.Strings(sel[mod])
	}
	return sel
}

func modules(sel map[string][]string) []string {
	mods := make([]string, 0, len(sel))
	for m := range sel {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	return mods
}

func runTool(dir, name string, args ...string) (stdout, stderr []byte, code int, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return o.Bytes(), e.Bytes(), ee.ExitCode(), nil
	}
	return o.Bytes(), e.Bytes(), 0, err
}

func runTests(root, _, out string, sel map[string][]string, _ map[string]string) ([]evidence, error) {
	suites := xmlSuites{Suites: []xmlSuite{}}
	for _, mod := range modules(sel) {
		args := append([]string{"test", "-race", "-shuffle=on", "-json"}, sel[mod]...)
		stdout, stderr, code, err := runTool(filepath.Join(root, mod), "go", args...)
		if err != nil {
			return nil, err
		}
		events := parseEvents(stdout)
		if code != 0 && len(events) == 0 {
			suites.Suites = append(suites.Suites, errorSuite("module "+mod, capText(stderr)))
			continue
		}
		suites.Suites = append(suites.Suites, toJUnit(events).Suites...)
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "junit.xml"), append([]byte(xml.Header), data...), 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "test.junit", Path: "junit.xml"}}, nil
}

func golangciVersion(root string, versions map[string]string) error {
	v, err := toolOutput(root, "golangci-lint", "version", "--short")
	if err != nil {
		return err
	}
	versions["golangci-lint"] = v
	return nil
}

func golangci(root, mod, tmp, config string, pkgs []string) ([]sarifResult, error) {
	f, err := os.CreateTemp(tmp, "*.sarif")
	if err != nil {
		return nil, err
	}
	sf := f.Name()
	if err := f.Close(); err != nil {
		return nil, err
	}
	args := []string{"run", "--allow-serial-runners", "--path-mode", "abs", "--max-issues-per-linter", "0", "--max-same-issues", "0", "--output.sarif.path", sf, "--output.text.path", "stderr"}
	if config != "" {
		args = append(args, "--config", config)
	}
	_, stderr, code, err := runTool(filepath.Join(root, mod), "golangci-lint", append(args, pkgs...)...)
	if err != nil {
		return nil, err
	}
	return lintResults(root, mod, sf, stderr, code == 0 || code == 1), nil
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

func runLint(root, _, out string, sel map[string][]string, versions map[string]string) ([]evidence, error) {
	if err := golangciVersion(root, versions); err != nil {
		return nil, err
	}
	vet, lint := newRun("go vet"), newRun("golangci-lint")
	tmp, err := os.MkdirTemp("", "assure-lint-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, mod := range modules(sel) {
		_, stderr, code, err := runTool(filepath.Join(root, mod), "go", append([]string{"vet"}, sel[mod]...)...)
		if err != nil {
			return nil, err
		}
		vet.Results = append(vet.Results, vetResults(root, mod, stderr, code != 0)...)
		rs, err := golangci(root, mod, tmp, "", sel[mod])
		if err != nil {
			return nil, err
		}
		lint.Results = append(lint.Results, rs...)
	}
	return writeSARIF(out, vet, lint)
}

type pack struct {
	name   string
	config string
}

var checkReturnsPack = pack{"check-returns/v1", `version: "2"
linters:
  default: none
  enable: [errcheck]
`}

var resourceBoundsPack = pack{"resource-bounds/v1", `version: "2"
linters:
  default: none
  enable: [bodyclose, noctx, gosec]
  settings:
    gosec:
      includes: [G110, G112, G114]
`}

func (p pack) run(root, _, out string, sel map[string][]string, versions map[string]string) ([]evidence, error) {
	if err := golangciVersion(root, versions); err != nil {
		return nil, err
	}
	config := filepath.Join(out, "golangci.yml")
	if err := os.WriteFile(config, []byte(p.config), 0o644); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "assure-pack-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	r := newRun("golangci-lint " + p.name)
	for _, mod := range modules(sel) {
		rs, err := golangci(root, mod, tmp, config, sel[mod])
		if err != nil {
			return nil, err
		}
		r.Results = append(r.Results, rs...)
	}
	return writeSARIF(out, r)
}
