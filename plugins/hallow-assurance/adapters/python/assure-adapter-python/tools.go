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

type pyTool struct {
	name, lo, hi string
	bins         []string
}

var pyTools = []pyTool{
	{name: "cosmic-ray", lo: "8.7.0", hi: "9.0.0", bins: []string{"cosmic-ray", "cr-filter-operators", "cr-filter-lines"}},
	{name: "coverage", lo: "7.13.0", hi: "8.0.0"},
	{name: "hypothesis", lo: "6.130.0", hi: "7.0.0"},
	{name: "parso", lo: "0.8.7", hi: "0.9.0"},
	{name: "pytest", lo: "9.0.0", hi: "10.0.0"},
	{name: "pytest-cov", lo: "7.0.0", hi: "8.0.0"},
	{name: "pytest-randomly", lo: "5.0.0", hi: "6.0.0"},
}

var objectiveTools = map[string][]string{
	"VER-TESTS-PASS":          {"pytest", "pytest-randomly", "hypothesis"},
	"VER-MUTATION-CHANGED":    {"pytest", "pytest-randomly", "pytest-cov", "coverage", "hypothesis", "cosmic-ray", "parso"},
	"VER-FAIL-ON-BASE":        {"pytest", "pytest-randomly", "hypothesis"},
	"VER-ROBUST-FUZZ":         {"pytest", "pytest-randomly", "hypothesis"},
	"VER-COVERAGE-RESOLUTION": {"pytest", "pytest-randomly", "coverage", "hypothesis"},
}

var exactVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*(\.post[0-9]+)?$`)

func isExact(v string) bool {
	return exactVersion.MatchString(v)
}

func compareNumeric(a, b string) int {
	x, y := strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y))
}

func splitVersion(v string) ([]string, string) {
	release, post, ok := strings.Cut(v, ".post")
	if !ok {
		post = ""
	}
	return strings.Split(release, "."), post
}

func compareVersions(a, b string) int {
	ar, ap := splitVersion(a)
	br, bp := splitVersion(b)
	for i := range max(len(ar), len(br)) {
		x, y := "0", "0"
		if i < len(ar) {
			x = ar[i]
		}
		if i < len(br) {
			y = br[i]
		}
		if c := compareNumeric(x, y); c != 0 {
			return c
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return -1
	case bp == "":
		return 1
	}
	return compareNumeric(ap, bp)
}

func (t pyTool) inRange(v string) bool {
	return isExact(v) && compareVersions(v, t.lo) >= 0 && compareVersions(v, t.hi) < 0
}

func (t pyTool) rangeText() string {
	return fmt.Sprintf(">= %s, < %s", t.lo, t.hi)
}

const installUv = "uv sync --locked"

var foreignLockfiles = []string{"poetry.lock", "pdm.lock", "Pipfile.lock"}

type lockfile struct {
	dir, rel, problem string
	untracked         bool
}

func relSlash(top, p string) string {
	rel, err := filepath.Rel(top, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
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

func findLockfile(top, unit string) (lockfile, error) {
	p, ok := findUp(top, unit, "uv.lock")
	if !ok {
		return lockfile{problem: fmt.Sprintf("no uv.lock in %s or any parent up to the git top-level; assure-adapter-python supports only uv projects, so the layout needs a committed uv.lock", relSlash(top, unit))}, nil
	}
	dir := filepath.Dir(p)
	for _, n := range foreignLockfiles {
		if exists(filepath.Join(dir, n)) {
			return lockfile{problem: fmt.Sprintf("%s sits beside uv.lock; assure-adapter-python supports only uv projects, so CI could not tell which install to reproduce", relSlash(top, filepath.Join(dir, n)))}, nil
		}
	}
	tracked, err := gitPaths(dir, "ls-files", "uv.lock")
	if err != nil {
		return lockfile{}, err
	}
	return lockfile{dir: dir, rel: relSlash(top, p), untracked: !slices.Contains(tracked, "uv.lock")}, nil
}

type venv struct {
	dir, problem string
	absent       bool
}

func findVenv(top, unit string, lock lockfile) venv {
	if env := os.Getenv("UV_PROJECT_ENVIRONMENT"); env != "" {
		dir := env
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(lock.dir, dir)
		}
		if !exists(dir) {
			return venv{absent: true, problem: fmt.Sprintf("UV_PROJECT_ENVIRONMENT names %s, which does not exist; run %s (in %s)", dir, installUv, relSlash(top, lock.dir))}
		}
		return checkVenv(top, dir, lock)
	}
	dir, ok := findUp(top, unit, ".venv")
	if !ok {
		return venv{absent: true, problem: fmt.Sprintf("no .venv in %s or any parent up to the git top-level; run %s (in %s)", relSlash(top, unit), installUv, relSlash(top, lock.dir))}
	}
	return checkVenv(top, dir, lock)
}

func checkVenv(top, dir string, lock lockfile) venv {
	if !exists(filepath.Join(dir, "bin", "python")) {
		return venv{problem: fmt.Sprintf("%s has no bin/python; run %s (in %s)", dir, installUv, relSlash(top, lock.dir))}
	}
	return venv{dir: dir}
}

func sitePackages(v string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(v, "lib", "python*", "site-packages"))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("%s holds %d site-packages directories under lib/python*/, want exactly one", v, len(matches))
	}
	return matches[0], nil
}

var normalizeRun = regexp.MustCompile(`[-_.]+`)

func normalizeName(n string) string {
	return strings.ToLower(normalizeRun.ReplaceAllString(n, "-"))
}

func distInfos(site, name string) ([]string, error) {
	entries, err := os.ReadDir(site)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), ".dist-info")
		if !ok || !e.IsDir() {
			continue
		}
		i := strings.LastIndex(base, "-")
		if i < 0 {
			continue
		}
		if normalizeName(base[:i]) == normalizeName(name) {
			out = append(out, filepath.Join(site, e.Name()))
		}
	}
	return out, nil
}

func metadataVersion(distInfo string) (string, error) {
	f, err := os.Open(filepath.Join(distInfo, "METADATA"))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Version:"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("METADATA has no Version field")
}

type resolved struct {
	name, version, pinnedBy string
	bins                    map[string]string
	untracked               bool
}

type resolution int

const (
	toolResolved resolution = iota
	toolNotInstalled
	toolFailed
)

func resolveTool(top string, lock lockfile, site string, t pyTool) (resolved, resolution, string) {
	fail := func(format string, args ...any) (resolved, resolution, string) {
		return resolved{}, toolFailed, fmt.Sprintf("%s: %s; install with: %s (in %s)", t.name, fmt.Sprintf(format, args...), installUv, relSlash(top, lock.dir))
	}
	infos, err := distInfos(site, t.name)
	if err != nil {
		return fail("%v", err)
	}
	switch len(infos) {
	case 0:
		r, _, problem := fail("not installed in %s", site)
		return r, toolNotInstalled, problem
	case 1:
	default:
		names := make([]string, len(infos))
		for i, p := range infos {
			names[i] = filepath.Base(p)
		}
		return fail("more than one install in %s (%s)", site, strings.Join(names, ", "))
	}
	v, err := metadataVersion(infos[0])
	if err != nil {
		return fail("%s: %v", infos[0], err)
	}
	if !isExact(v) {
		return fail("installed version %q is not an exact version", v)
	}
	if !t.inRange(v) {
		return fail("installed version %s is outside the supported range %s", v, t.rangeText())
	}
	r := resolved{name: t.name, version: v, pinnedBy: lock.rel, untracked: lock.untracked, bins: map[string]string{}}
	venvDir := filepath.Dir(filepath.Dir(filepath.Dir(site)))
	for _, b := range t.bins {
		p := filepath.Join(venvDir, "bin", b)
		if !exists(p) {
			return fail("executable %s is missing from %s", b, filepath.Join(venvDir, "bin"))
		}
		r.bins[b] = p
	}
	return r, toolResolved, ""
}

type environment struct {
	lock lockfile
	venv string
}

func resolveEnvironment(top, unit string) (environment, string, error) {
	lock, err := findLockfile(top, unit)
	if err != nil {
		return environment{}, "", err
	}
	if lock.problem != "" {
		return environment{}, lock.problem, nil
	}
	v := findVenv(top, unit, lock)
	if v.problem != "" {
		return environment{}, v.problem, nil
	}
	return environment{lock: lock, venv: v.dir}, "", nil
}

func resolveTools(top string, env environment, names []string) ([]resolved, string, error) {
	site, err := sitePackages(env.venv)
	if err != nil {
		return nil, fmt.Sprintf("%v; run %s (in %s)", err, installUv, relSlash(top, env.lock.dir)), nil
	}
	var out []resolved
	var problems []string
	for _, t := range pyTools {
		if !slices.Contains(names, t.name) {
			continue
		}
		r, _, problem := resolveTool(top, env.lock, site, t)
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
	return toolsResponse{Protocol: 2, Tools: []toolEntry{}, Environment: &environmentMessage{Message: msg}}
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
		return toolsEnvironment(fmt.Sprintf("%s has no pyproject.toml in it or any parent up to the git top-level; assure-adapter-python needs one to find its unit", relSlash(top, root))), nil
	}
	lock, err := findLockfile(top, unit)
	if err != nil {
		return toolsResponse{}, err
	}
	if lock.problem != "" {
		return toolsEnvironment(lock.problem), nil
	}
	if lock.dir != root {
		return toolsEnvironment(fmt.Sprintf("the lockfile is %s, outside %s; run assure tools from %s so its install line runs where the lockfile is", lock.rel, relSlash(top, root), relSlash(top, lock.dir))), nil
	}
	if !onPath("uv") {
		return toolsEnvironment(fmt.Sprintf("uv is not on PATH, so CI cannot run %s; pin uv in the repository's mise configuration", installUv)), nil
	}
	tools, problem := installedTools(top, unit, lock)
	if problem != "" {
		return toolsEnvironment(problem), nil
	}
	slices.SortFunc(tools, func(a, b toolEntry) int { return strings.Compare(a.Name, b.Name) })
	return toolsResponse{Protocol: 2, Tools: tools}, nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func installedTools(top, unit string, lock lockfile) ([]toolEntry, string) {
	notInstalled := func(t pyTool) toolEntry {
		return toolEntry{Name: t.name, PinnedBy: lock.rel, Install: installUv, untracked: lock.untracked}
	}
	tools := []toolEntry{}
	v := findVenv(top, unit, lock)
	if v.absent {
		for _, t := range pyTools {
			tools = append(tools, notInstalled(t))
		}
		return tools, ""
	}
	if v.problem != "" {
		return nil, v.problem
	}
	site, err := sitePackages(v.dir)
	if err != nil {
		return nil, fmt.Sprintf("%v; run %s (in %s)", err, installUv, relSlash(top, lock.dir))
	}
	var problems []string
	for _, t := range pyTools {
		r, res, problem := resolveTool(top, lock, site, t)
		switch res {
		case toolResolved:
			tools = append(tools, toolEntry{Name: r.name, Version: r.version, PinnedBy: r.pinnedBy, Install: installUv, untracked: r.untracked})
		case toolNotInstalled:
			tools = append(tools, notInstalled(t))
		case toolFailed:
			problems = append(problems, problem)
		}
	}
	if len(problems) > 0 {
		return nil, strings.Join(problems, "\n")
	}
	return tools, ""
}

func toolsOp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "repository directory to resolve tools for")
	if err := fs.Parse(args); err != nil || *root == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-python tools --root <dir>")
		return 2
	}
	resp, err := listTools(*root)
	if err == nil {
		err = json.NewEncoder(stdout).Encode(resp)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-python: tools: %v\n", err)
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

func seedArgs(seed string) []string {
	return []string{"-p", "randomly", "-p", "hypothesispytest", "--randomly-seed=" + seed, "--hypothesis-seed=" + seed}
}
