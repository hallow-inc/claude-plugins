package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

type tsTool struct {
	name, bin, lo, hi string
}

var tsTools = []tsTool{
	{name: "@stryker-mutator/core", bin: "stryker", lo: "10.0.0", hi: "11.0.0"},
	{name: "@stryker-mutator/vitest-runner", lo: "10.0.0", hi: "11.0.0"},
	{name: "@vitest/coverage-v8", lo: "5.0.0", hi: "6.0.0"},
	{name: "fast-check", lo: "4.0.0", hi: "5.0.0"},
	{name: "vitest", bin: "vitest", lo: "5.0.0", hi: "6.0.0"},
}

var objectiveTools = map[string][]string{
	"VER-TESTS-PASS":          {"vitest"},
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
	untracked                  bool
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
	tracked, err := adapterkit.GitPaths(dir, "ls-files", name)
	if err != nil {
		return lockfile{}, err
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
	return lockfile{dir: dir, rel: rel, install: install, untracked: !slices.Contains(tracked, name)}, nil
}

type resolved struct {
	name, version, pinnedBy, install, bin string
	untracked                             bool
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

type resolution int

const (
	toolResolved resolution = iota
	toolNotInstalled
	toolFailed
)

func resolveTool(top, unit string, lock lockfile, t tsTool) (resolved, resolution, string) {
	fail := func(format string, args ...any) (resolved, resolution, string) {
		msg := t.name + ": " + fmt.Sprintf(format, args...)
		if lock.problem != "" {
			return resolved{}, toolFailed, msg
		}
		return resolved{}, toolFailed, fmt.Sprintf("%s; install with: %s (in %s)", msg, lock.install, relSlash(top, lock.dir))
	}
	if lock.problem != "" {
		return fail("%s", lock.problem)
	}
	pkgJSON, ok := findUp(top, unit, filepath.Join("node_modules", filepath.FromSlash(t.name), "package.json"))
	if !ok {
		r, _, problem := fail("not installed in node_modules of %s or any parent up to the git top-level", relSlash(top, unit))
		return r, toolNotInstalled, problem
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
	r := resolved{name: t.name, version: v, pinnedBy: lock.rel, install: lock.install, untracked: lock.untracked}
	if t.bin != "" {
		bin, ok := findUp(top, unit, filepath.Join("node_modules", ".bin", t.bin))
		if !ok {
			return fail("executable %s is missing from node_modules/.bin", t.bin)
		}
		r.bin = bin
	}
	return r, toolResolved, ""
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
		r, _, problem := resolveTool(top, unit, lock, t)
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
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	PinnedBy  string `json:"pinned_by"`
	Install   string `json:"install"`
	untracked bool
}

func (e toolEntry) MarshalJSON() ([]byte, error) {
	type plain toolEntry
	return json.Marshal(struct {
		plain
		Untracked bool `json:"untracked,omitempty"`
	}{plain(e), e.untracked})
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

func toolsEnvironment(msg string) toolsResponse {
	return toolsResponse{Protocol: 2, Tools: []toolEntry{}, Environment: &environmentMessage{Message: msg}}
}

func listTools(root string) (toolsResponse, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return toolsResponse{}, err
	}
	root = realPath(root)
	top, err := adapterkit.ToolOutput(root, "git", "rev-parse", "--show-toplevel")
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
	resp := toolsResponse{Protocol: 2, Tools: []toolEntry{}}
	var problems []string
	for _, t := range tsTools {
		r, res, problem := resolveTool(top, unit, lock, t)
		switch res {
		case toolResolved:
			resp.Tools = append(resp.Tools, toolEntry{Name: r.name, Version: r.version, PinnedBy: r.pinnedBy, Install: r.install, untracked: r.untracked})
		case toolNotInstalled:
			resp.Tools = append(resp.Tools, toolEntry{Name: t.name, PinnedBy: lock.rel, Install: lock.install, untracked: lock.untracked})
		case toolFailed:
			problems = append(problems, problem)
		}
	}
	if len(problems) > 0 {
		return toolsEnvironment(strings.Join(problems, "\n")), nil
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
	sha, err := adapterkit.ToolOutput(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return seedFrom(sha)
}
