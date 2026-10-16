package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type tsTool struct {
	name, bin, lo, hi string
}

var tsTools = []tsTool{
	{name: "@stryker-mutator/core", bin: "stryker", lo: "10.0.0", hi: "11.0.0"},
	{name: "@stryker-mutator/vitest-runner", lo: "10.0.0", hi: "11.0.0"},
	{name: "@vitest/coverage-v8", lo: "5.0.0", hi: "6.0.0"},
	{name: "eslint", bin: "eslint", lo: "10.0.0", hi: "11.0.0"},
	{name: "fast-check", lo: "4.0.0", hi: "5.0.0"},
	{name: "typescript", bin: "tsc", lo: "5.0.0", hi: "6.1.0"},
	{name: "typescript-eslint", lo: "8.0.0", hi: "9.0.0"},
	{name: "vitest", bin: "vitest", lo: "5.0.0", hi: "6.0.0"},
}

var lintTools = []string{"eslint", "typescript", "typescript-eslint"}

var objectiveTools = map[string][]string{
	"VER-TESTS-PASS":          {"vitest"},
	"CODE-ZERO-WARNINGS":      {"eslint", "typescript"},
	"CODE-CHECK-RETURNS":      lintTools,
	"CODE-RESOURCE-BOUNDS":    lintTools,
	"CODE-COMPLEXITY":         lintTools,
	"CODE-NO-UNSAFE":          lintTools,
	"VER-MUTATION-CHANGED":    {"@stryker-mutator/core", "@stryker-mutator/vitest-runner", "vitest"},
	"VER-FAIL-ON-BASE":        {"vitest"},
	"VER-ROBUST-FUZZ":         {"fast-check", "vitest"},
	"VER-COVERAGE-RESOLUTION": {"@vitest/coverage-v8", "vitest"},
}

var exactVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func isExact(v string) bool {
	return exactVersion.MatchString(v)
}

func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, y := strings.TrimLeft(as[i], "0"), strings.TrimLeft(bs[i], "0")
		if c := cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y)); c != 0 {
			return c
		}
	}
	return 0
}

func (t tsTool) inRange(v string) bool {
	return isExact(v) && compareVersions(v, t.lo) >= 0 && compareVersions(v, t.hi) < 0
}

func (t tsTool) rangeText() string {
	return fmt.Sprintf(">= %s, < %s", t.lo, t.hi)
}

const (
	installNpm       = "npm ci"
	installPnpm      = "pnpm install --frozen-lockfile"
	installYarnBerry = "yarn install --immutable"
	installYarn      = "yarn install --frozen-lockfile"
)

var lockfileNames = []string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock"}

func installFor(name string, r io.Reader) (string, error) {
	switch name {
	case "package-lock.json":
		return installNpm, nil
	case "pnpm-lock.yaml":
		return installPnpm, nil
	}
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, long, err := br.ReadLine()
		if errors.Is(err, io.EOF) {
			return installYarn, nil
		}
		if err != nil {
			return "", err
		}
		if !long && string(line) == "__metadata:" {
			return installYarnBerry, nil
		}
		for long {
			_, long, err = br.ReadLine()
			if errors.Is(err, io.EOF) {
				return installYarn, nil
			}
			if err != nil {
				return "", err
			}
		}
	}
}

type lockfile struct {
	dir, rel, install, problem string
}

func relSlash(top, p string) string {
	rel, err := filepath.Rel(top, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func findLockfile(top, unit string) (lockfile, error) {
	for dir := unit; ; dir = filepath.Dir(dir) {
		var here []string
		for _, n := range lockfileNames {
			if exists(filepath.Join(dir, n)) {
				here = append(here, n)
			}
		}
		if len(here) > 1 {
			return lockfile{problem: fmt.Sprintf("%s holds more than one lockfile (%s); keep the one your package manager uses, because CI could not tell which install to reproduce", relSlash(top, dir), strings.Join(here, ", "))}, nil
		} else if len(here) == 1 {
			return lockfileIn(top, dir, here[0])
		}
		if dir == top || filepath.Dir(dir) == dir {
			return lockfile{problem: fmt.Sprintf("no lockfile (%s) in %s or any parent up to the git top-level, so CI could not reproduce the install; commit the lockfile your package manager writes", strings.Join(lockfileNames, ", "), relSlash(top, unit))}, nil
		}
	}
}

func lockfileIn(top, dir, name string) (lockfile, error) {
	rel := relSlash(top, filepath.Join(dir, name))
	tracked, err := gitPaths(dir, "ls-files", name)
	if err != nil {
		return lockfile{}, err
	}
	if !slices.Contains(tracked, name) {
		return lockfile{problem: fmt.Sprintf("%s is not tracked by git, so CI would not see it; commit it", rel)}, nil
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return lockfile{}, err
	}
	defer func() { _ = f.Close() }()
	install, err := installFor(name, f)
	if err != nil {
		return lockfile{}, fmt.Errorf("%s: %w", rel, err)
	}
	return lockfile{dir: dir, rel: rel, install: install}, nil
}

type resolved struct {
	name, version, pinnedBy, install, bin string
}

func findUp(top, unit, rel string) (string, bool) {
	for dir := unit; ; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, rel)
		if exists(p) {
			return p, true
		}
		if dir == top || filepath.Dir(dir) == dir {
			return "", false
		}
	}
}

func installedVersion(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	return pkg.Version, nil
}

func resolveTool(top, unit string, lock lockfile, t tsTool) (resolved, string) {
	fail := func(format string, args ...any) (resolved, string) {
		msg := t.name + ": " + fmt.Sprintf(format, args...)
		if lock.problem != "" {
			return resolved{}, msg
		}
		return resolved{}, fmt.Sprintf("%s; install with: %s (in %s)", msg, lock.install, relSlash(top, lock.dir))
	}
	if lock.problem != "" {
		return fail("%s", lock.problem)
	}
	pkgJSON, ok := findUp(top, unit, filepath.Join("node_modules", filepath.FromSlash(t.name), "package.json"))
	if !ok {
		return fail("not installed in node_modules of %s or any parent up to the git top-level", relSlash(top, unit))
	}
	v, err := installedVersion(pkgJSON)
	if err != nil {
		return fail("%s is unreadable: %v", relSlash(top, pkgJSON), err)
	}
	if !isExact(v) {
		return fail("installed version %q is not an exact version", v)
	}
	if !t.inRange(v) {
		return fail("installed version %s is outside the supported range %s", v, t.rangeText())
	}
	r := resolved{name: t.name, version: v, pinnedBy: lock.rel, install: lock.install}
	if t.bin != "" {
		bin, ok := findUp(top, unit, filepath.Join("node_modules", ".bin", t.bin))
		if !ok {
			return fail("executable %s is missing from node_modules/.bin", t.bin)
		}
		r.bin = bin
	}
	return r, ""
}

func resolveTools(top, unit string, names []string) ([]resolved, string, error) {
	lock, err := findLockfile(top, unit)
	if err != nil {
		return nil, "", err
	}
	var out []resolved
	var problems []string
	for _, t := range tsTools {
		if !slices.Contains(names, t.name) {
			continue
		}
		r, problem := resolveTool(top, unit, lock, t)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		out = append(out, r)
	}
	if len(problems) > 0 {
		return nil, strings.Join(problems, "\n"), nil
	}
	return out, "", nil
}

type environmentMessage struct {
	Message string `json:"message"`
}

type toolEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	PinnedBy string `json:"pinned_by"`
	Install  string `json:"install"`
}

type toolsResponse struct {
	Protocol    int                 `json:"protocol"`
	Tools       []toolEntry         `json:"tools"`
	Environment *environmentMessage `json:"environment,omitempty"`
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func gitOutput(dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git not found on PATH")
	}
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}

func toolsEnvironment(msg string) toolsResponse {
	return toolsResponse{Protocol: 1, Tools: []toolEntry{}, Environment: &environmentMessage{Message: msg}}
}

func listTools(root string) (toolsResponse, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return toolsResponse{}, err
	}
	root = realPath(root)
	top, err := gitOutput(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return toolsResponse{}, err
	}
	top = realPath(top)
	unit, ok := unitOf(top, root)
	if !ok {
		return toolsEnvironment(fmt.Sprintf("%s has no package.json in it or any parent up to the git top-level; assure-adapter-typescript needs one to find its unit", relSlash(top, root))), nil
	}
	if msg := refusal(top, unit); msg != "" {
		return toolsEnvironment(msg), nil
	}
	lock, err := findLockfile(top, unit)
	if err != nil {
		return toolsResponse{}, err
	}
	if lock.problem == "" && lock.dir != root {
		return toolsEnvironment(fmt.Sprintf("the lockfile is %s, outside %s; run assure tools from %s so its install line runs where the lockfile is", lock.rel, relSlash(top, root), relSlash(top, lock.dir))), nil
	}
	names := make([]string, 0, len(tsTools))
	for _, t := range tsTools {
		names = append(names, t.name)
	}
	rs, env, err := resolveTools(top, unit, names)
	if err != nil {
		return toolsResponse{}, err
	}
	if env != "" {
		return toolsEnvironment(env), nil
	}
	resp := toolsResponse{Protocol: 1, Tools: []toolEntry{}}
	for _, r := range rs {
		resp.Tools = append(resp.Tools, toolEntry{Name: r.name, Version: r.version, PinnedBy: r.pinnedBy, Install: r.install})
	}
	slices.SortFunc(resp.Tools, func(a, b toolEntry) int { return strings.Compare(a.Name, b.Name) })
	return resp, nil
}

func toolsOp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "repository directory to resolve tools for")
	if err := fs.Parse(args); err != nil || *root == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-typescript tools --root <dir>")
		return 2
	}
	resp, err := listTools(*root)
	if err == nil {
		err = json.NewEncoder(stdout).Encode(resp)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: tools: %v\n", err)
		return 1
	}
	return 0
}

const maxSeed = 2147483647

func seedFrom(sha string) (string, error) {
	if len(sha) < 16 {
		return "", fmt.Errorf("git rev-parse HEAD: unexpected output %q", sha)
	}
	n, err := strconv.ParseUint(sha[:16], 16, 64)
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strconv.FormatUint(n%maxSeed+1, 10), nil
}

func assureSeed(root string) (string, error) {
	sha, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return seedFrom(sha)
}
