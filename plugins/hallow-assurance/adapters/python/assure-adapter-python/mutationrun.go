package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type mutationTarget struct {
	rel    string
	ranges []lineRange
}

func mutationTargets(c runContext, unit string) ([]mutationTarget, error) {
	sources, err := changedSources(c, unit)
	if err != nil || len(sources) == 0 {
		return nil, err
	}
	tracked, err := gitPaths(c.root, append([]string{"ls-files", "--"}, sources...)...)
	if err != nil {
		return nil, err
	}
	var out []mutationTarget
	for _, f := range sources {
		if !slices.Contains(tracked, f) {
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
		if len(ranges) > 0 {
			out = append(out, mutationTarget{rel: f, ranges: ranges})
		}
	}
	return out, nil
}

func syntaxGuard(python, unit string, abs []string) (string, error) {
	out := map[string]struct {
		Error string `json:"error"`
	}{}
	if err := runHelper(python, unit, "syntax", abs, &out); err != nil {
		return "", err
	}
	var problems []string
	for _, p := range abs {
		if e := out[p].Error; e != "" {
			problems = append(problems, e+"; cosmic-ray's parser (parso) cannot read this syntax, so no mutant would be trustworthy")
		}
	}
	return strings.Join(problems, "\n"), nil
}

type contextFile struct {
	Contexts map[string][]string `json:"contexts"`
}

// testSelection is the pytest @argsfile for a file's mutants: the tests that executed a changed
// line, or "" for the whole suite when a changed line ran only outside any test (import time).
func testSelection(f contextFile, ranges []lineRange) (ids []string, whole, covered bool) {
	set := map[string]bool{}
	for line, ctxs := range f.Contexts {
		var n int
		if _, err := fmt.Sscan(line, &n); err != nil || !inRanges(ranges, n) {
			continue
		}
		for _, ctx := range ctxs {
			if id, _, ok := strings.Cut(ctx, "|"); ok && id != "" {
				set[id] = true
			} else {
				whole = true
			}
		}
		covered = covered || len(ctxs) > 0
	}
	return slices.Sorted(maps.Keys(set)), whole, covered
}

type mutationTree struct {
	dir, tree, unit string
	env             []string
}

func (c runContext) mutationTree(unit, tmp string) (mutationTree, string, error) {
	t := mutationTree{dir: tmp, tree: filepath.Join(tmp, "tree")}
	if err := workTree(c.top, unit, t.tree); err != nil {
		return t, "", err
	}
	t.unit = filepath.Join(t.tree, filepath.FromSlash(relSlash(c.top, unit)))
	if msg, err := probeTree(c.units[unit].python, c.top, t.tree, t.unit); err != nil || msg != "" {
		return t, msg, err
	}
	pyc := filepath.Join(tmp, "pycache")
	if err := os.MkdirAll(pyc, 0o755); err != nil {
		return t, "", err
	}
	t.env = append(treeEnv(t.unit), "ASSURE_SEED="+c.seed, "PYTHONDONTWRITEBYTECODE=1", "PYTHONPYCACHEPREFIX="+pyc)
	return t, "", nil
}

func (c runContext) contexts(unit string, t mutationTree, targets []mutationTarget) (map[string]contextFile, error) {
	python := c.units[unit].python
	var abs []string
	for _, tg := range targets {
		abs = append(abs, filepath.Join(t.tree, filepath.FromSlash(relSlash(c.top, filepath.Join(c.root, filepath.FromSlash(tg.rel))))))
	}
	rc, data, report := filepath.Join(t.dir, "coveragerc"), filepath.Join(t.dir, "contexts.data"), filepath.Join(t.dir, "contexts.json")
	if err := os.WriteFile(rc, []byte(coverageRC(abs)), 0o644); err != nil {
		return nil, err
	}
	env := append(slices.Clone(t.env), "COVERAGE_FILE="+data)
	args := append(pytestArgs(c.seed, filepath.Join(t.dir, "contexts.xml")), "-p", "pytest_cov", "--cov", "--cov-config="+rc, "--cov-context=test", "--cov-report=")
	stdout, stderr, code, err := runToolEnv(t.unit, env, python, args...)
	if err != nil {
		return nil, err
	}
	if !slices.Contains([]int{0, 1, 2, 5}, code) {
		return nil, fmt.Errorf("pytest in %s exited %d while recording which tests run the changed lines: %s", relSlash(c.top, unit), code, capText(append(stdout, stderr...)))
	}
	_, jerr, jcode, err := runToolEnv(t.unit, env, python, "-m", "coverage", "json", "--show-contexts", "--rcfile="+rc, "--data-file="+data, "-o", report)
	if err != nil {
		return nil, err
	}
	if jcode != 0 {
		return nil, fmt.Errorf("coverage json in %s exited %d: %s", relSlash(c.top, unit), jcode, capText(jerr))
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		return nil, err
	}
	var r struct {
		Files map[string]contextFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("coverage json in %s: %w", relSlash(c.top, unit), err)
	}
	out := map[string]contextFile{}
	for name, f := range r.Files {
		if !filepath.IsAbs(name) {
			name = filepath.Join(t.unit, name)
		}
		out[relSlash(t.tree, name)] = f
	}
	return out, nil
}

func (c runContext) baseline(unit string, t mutationTree, script string) (time.Duration, error) {
	started := time.Now()
	stdout, stderr, code, err := runToolEnv(t.unit, t.env, "sh", script)
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return 0, fmt.Errorf("the selected tests of %s fail before mutation (exit %d), so no mutant result would mean anything: %s", relSlash(c.top, unit), code, capText(append(stdout, stderr...)))
	}
	return time.Since(started), nil
}

func (c runContext) cosmic(unit string, t mutationTree, name string, args ...string) ([]byte, error) {
	bin := c.units[unit].tools["cosmic-ray"].bins[name]
	stdout, stderr, code, err := runToolEnv(t.unit, t.env, bin, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("%s %s exited %d: %s", name, strings.Join(args, " "), code, capText(append(stdout, stderr...)))
	}
	return stdout, nil
}

func (c runContext) mutateFile(unit string, t mutationTree, i int, tg mutationTarget, ctx contextFile) ([]strykerMutant, string, error) {
	treeRel := relSlash(c.top, filepath.Join(c.root, filepath.FromSlash(tg.rel)))
	abs := filepath.Join(t.tree, filepath.FromSlash(treeRel))
	orig, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(t.dir, fmt.Sprintf("session-%d", i))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	ids, whole, covered := testSelection(ctx, tg.ranges)
	script := filepath.Join(dir, "tests.sh")
	timeout := time.Minute
	if covered {
		argsFile := ""
		if !whole {
			argsFile = filepath.Join(dir, "tests.args")
			if err := os.WriteFile(argsFile, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
				return nil, "", err
			}
		}
		if err := os.WriteFile(script, []byte(testScript(c.units[unit].python, c.seed, argsFile)), 0o644); err != nil {
			return nil, "", err
		}
		took, err := c.baseline(unit, t, script)
		if err != nil {
			return nil, "", err
		}
		timeout = max(timeout, 20*took)
	} else if err := os.WriteFile(script, []byte("exit 0\n"), 0o644); err != nil {
		return nil, "", err
	}
	module := relSlash(t.unit, abs)
	cfg, session := filepath.Join(dir, "cosmic-ray.toml"), filepath.Join(dir, "session.sqlite")
	if err := os.WriteFile(cfg, []byte(cosmicConfig(module, "sh "+shellQuote(script), timeout, tg.ranges)), 0o644); err != nil {
		return nil, "", err
	}
	steps := [][]string{
		{"cosmic-ray", "init", cfg, session},
		{"cr-filter-operators", session, cfg},
		{"cr-filter-lines", "--config", cfg, session},
		{"cosmic-ray", "exec", cfg, session},
	}
	for _, s := range steps {
		if _, err := c.cosmic(unit, t, s[0], s[1:]...); err != nil {
			return nil, "", err
		}
	}
	dump, err := c.cosmic(unit, t, "cosmic-ray", "dump", session)
	if err != nil {
		return nil, "", err
	}
	muts, err := mutantsFromDump(tg.rel, string(orig), dump, tg.ranges, covered)
	return muts, string(orig), err
}

func (c runContext) mutateUnit(unit string, targets []mutationTarget, rep strykerReport) (string, error) {
	tmp, err := os.MkdirTemp("", "assure-mutation-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	t, msg, err := c.mutationTree(unit, realPath(tmp))
	if err != nil || msg != "" {
		return msg, err
	}
	ctxs, err := c.contexts(unit, t, targets)
	if err != nil {
		return "", err
	}
	for i, tg := range targets {
		muts, source, err := c.mutateFile(unit, t, i, tg, ctxs[relSlash(c.top, filepath.Join(c.root, filepath.FromSlash(tg.rel)))])
		if err != nil {
			return "", err
		}
		rep.Files[tg.rel] = &strykerFile{Language: "python", Source: source, Mutants: muts}
	}
	return "", nil
}

// guardedTargets lists every unit's mutation targets and checks all of them parse before any
// mutant runs anywhere.
func guardedTargets(c runContext) (map[string][]mutationTarget, string, error) {
	all := map[string][]mutationTarget{}
	var problems []string
	for _, unit := range slices.Sorted(maps.Keys(c.sel)) {
		targets, err := mutationTargets(c, unit)
		if err != nil || len(targets) == 0 {
			if err != nil {
				return nil, "", err
			}
			continue
		}
		var abs []string
		for _, tg := range targets {
			abs = append(abs, filepath.Join(c.root, filepath.FromSlash(tg.rel)))
		}
		msg, err := syntaxGuard(c.units[unit].python, unit, abs)
		if err != nil {
			return nil, "", err
		}
		if msg != "" {
			problems = append(problems, msg)
		}
		all[unit] = targets
	}
	return all, strings.Join(problems, "\n"), nil
}

func uniqueIDs(rep strykerReport) error {
	seen := map[string]string{}
	for _, f := range slices.Sorted(maps.Keys(rep.Files)) {
		for _, m := range rep.Files[f].Mutants {
			if other, dup := seen[m.ID]; dup {
				return fmt.Errorf("mutation report: two mutants have id %s (%s and %s)", m.ID, other, f)
			}
			seen[m.ID] = f
		}
	}
	return nil
}

func runMutation(c runContext) ([]evidence, string, error) {
	all, msg, err := guardedTargets(c)
	if err != nil || msg != "" {
		return nil, msg, err
	}
	rep := newStrykerReport()
	for _, unit := range slices.Sorted(maps.Keys(all)) {
		if msg, err := c.mutateUnit(unit, all[unit], rep); err != nil || msg != "" {
			return nil, msg, err
		}
	}
	if err := uniqueIDs(rep); err != nil {
		return nil, "", err
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
