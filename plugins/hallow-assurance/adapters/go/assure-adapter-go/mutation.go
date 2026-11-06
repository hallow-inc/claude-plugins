package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

// At 10 with 2 workers, internal/hookio still reported 15 false TIMED OUT that hid 9 survivors;
// 100 measured clean. A genuinely looping mutant waits longer, but a false kill is worse.
const timeoutCoefficient = 100

// Gremlins' default of one worker per CPU starved test runs past the timeout on a 12-core
// machine (33 of 47 mutants TIMED OUT, hiding 3 survivors); 2 measured clean.
const workers = 2

var strykerStatus = map[string]string{
	"KILLED":      "Killed",
	"LIVED":       "Survived",
	"NOT COVERED": "NoCoverage",
	"TIMED OUT":   "Timeout",
	"NOT VIABLE":  "CompileError",
	"SKIPPED":     "Ignored",
}

type gremlinsFile struct {
	FileName  string `json:"file_name"`
	Mutations []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"mutations"`
}

type gremlinsOutput struct {
	Files []gremlinsFile `json:"files"`
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

func parseGremlins(data []byte) (gremlinsOutput, error) {
	var g gremlinsOutput
	if err := json.Unmarshal(data, &g); err != nil {
		return g, fmt.Errorf("gremlins output: %w", err)
	}
	return g, nil
}

func (r strykerReport) addGremlins(data []byte, modRel string, source func(repoPath string) (string, error)) error {
	g, err := parseGremlins(data)
	if err != nil {
		return err
	}
	return r.add(g, modRel, source)
}

func (r strykerReport) add(g gremlinsOutput, modRel string, source func(repoPath string) (string, error)) error {
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

func (r strykerReport) keepScoped(s adapterkit.Scope) {
	for p, f := range r.Files {
		kept := f.Mutants[:0]
		for _, m := range f.Mutants {
			if s.Contains(p, m.Location.Start.Line) {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			delete(r.Files, p)
			continue
		}
		f.Mutants = kept
	}
}

func rapidSeed(root string) (string, error) {
	sha, err := adapterkit.ToolOutput(root, "git", "rev-parse", "HEAD")
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
	out, err := adapterkit.ToolOutput(".", "go", "version", "-m", bin)
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

func runMutation(root string, args adapterkit.RunArgs, out string, sel map[string][]string, _ map[string]string) ([]evidence, error) {
	seed, err := rapidSeed(root)
	if err != nil {
		return nil, err
	}
	rep := newStrykerReport()
	source := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		return string(b), err
	}
	if args.Scope != nil {
		err = rep.mutateScoped(root, sel, out, seed, source)
		rep.keepScoped(args.Scope)
	} else {
		err = rep.mutateChanged(root, args.ChangedFrom, out, seed, source)
	}
	if err != nil {
		return nil, err
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

func (r strykerReport) mutateScoped(root string, sel map[string][]string, out, seed string, source func(string) (string, error)) error {
	pkgs := map[string][]string{}
	for mod, targets := range sel {
		if slices.Contains(targets, "./...") {
			dirs, err := modulePackages(filepath.Join(root, mod))
			if err != nil {
				return err
			}
			pkgs[mod] = dirs
			continue
		}
		for _, t := range targets {
			pkgs[mod] = append(pkgs[mod], path.Clean(t))
		}
	}
	return r.mutatePackages(root, "", pkgs, out, seed, source)
}

func (r strykerReport) mutateChanged(root, ref, out, seed string, source func(string) (string, error)) error {
	paths, err := adapterkit.ChangedPaths(root, ref)
	if err != nil {
		return err
	}
	return r.mutatePackages(root, ref, changedPackages(root, paths), out, seed, source)
}

func (r strykerReport) mutatePackages(root, ref string, pkgs map[string][]string, out, seed string, source func(string) (string, error)) error {
	for _, mod := range modules(pkgs) {
		for _, rel := range pkgs[mod] {
			dir := filepath.Join(root, mod, filepath.FromSlash(rel))
			if ok, err := buildable(dir); err != nil {
				return err
			} else if !ok {
				continue
			}
			data, err := unleash(dir, ref, dir, out, seed)
			if err != nil {
				return err
			}
			g, err := parseGremlins(data)
			if err != nil {
				return err
			}
			g.Files = slices.DeleteFunc(g.Files, func(f gremlinsFile) bool {
				return path.Dir(path.Clean(filepath.ToSlash(f.FileName))) != "."
			})
			if err := r.add(g, path.Join(mod, rel), source); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildable(dir string) (bool, error) {
	p, err := build.Default.ImportDir(dir, 0)
	if _, ok := errors.AsType[*build.NoGoError](err); ok {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(p.GoFiles)+len(p.CgoFiles) > 0, nil
}

func modulePackages(modDir string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(modDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == modDir {
			return nil
		}
		rel, err := filepath.Rel(modDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if ignoredByGo(rel + "/x.go") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !d.Type().IsRegular() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			return nil
		}
		if dir := path.Dir(rel); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
		return nil
	})
	slices.Sort(dirs)
	return dirs, err
}

func changedPackages(root string, paths []string) map[string][]string {
	set := map[string]map[string]bool{}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || ignoredByGo(p) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		dir := path.Dir(p)
		mod, ok := moduleOf(root, dir)
		if !ok {
			continue
		}
		rel := path.Clean(strings.TrimPrefix(strings.TrimPrefix(dir, mod), "/"))
		if set[mod] == nil {
			set[mod] = map[string]bool{}
		}
		set[mod][rel] = true
	}
	pkgs := map[string][]string{}
	for mod, ts := range set {
		for rel := range ts {
			pkgs[mod] = append(pkgs[mod], rel)
		}
		slices.Sort(pkgs[mod])
	}
	return pkgs
}

// Gremlins v0.6.0 tests a package main mutant with `go test <module root import path>` from its
// target directory, judging it by the root package's tests. The wrapper rewrites that argument
// to "." so the mutated package's own tests run. Delete it once a Gremlins release contains
// go-gremlins/gremlins#306.
const goWrapper = `#!/bin/sh
if [ "$1" = test ]; then
	for a in "$@"; do
		shift
		if [ "$a" = "$ASSURE_GO_MODULE" ]; then set -- "$@" .; else set -- "$@" "$a"; fi
	done
fi
exec "$ASSURE_REAL_GO" "$@"
`

func goWrapperEnv(dir string) (env []string, cleanup func(), err error) {
	if !onPath("go") {
		return nil, func() {}, nil
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		return nil, nil, err
	}
	if realGo, err = filepath.Abs(realGo); err != nil {
		return nil, nil, err
	}
	gomodFile := filepath.Join(dir, "go.mod")
	for _, err := os.Stat(gomodFile); err != nil; _, err = os.Stat(gomodFile) {
		parent := filepath.Dir(filepath.Dir(gomodFile))
		if parent == filepath.Dir(gomodFile) {
			return nil, nil, fmt.Errorf("no go.mod in or above %s", dir)
		}
		gomodFile = filepath.Join(parent, "go.mod")
	}
	mod, err := adapterkit.ToolOutput(dir, realGo, "mod", "edit", "-json", gomodFile)
	if err != nil {
		return nil, nil, err
	}
	var gomod struct{ Module struct{ Path string } }
	if err := json.Unmarshal([]byte(mod), &gomod); err != nil {
		return nil, nil, fmt.Errorf("go mod edit -json %s: %w", gomodFile, err)
	}
	if gomod.Module.Path == "" {
		return nil, nil, fmt.Errorf("go mod edit -json %s: no module path", gomodFile)
	}
	bin, err := os.MkdirTemp("", "assure-go-wrapper-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { _ = os.RemoveAll(bin) }
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(goWrapper), 0o755); err != nil {
		cleanup()
		return nil, nil, err
	}
	return []string{"ASSURE_REAL_GO=" + realGo, "ASSURE_GO_MODULE=" + gomod.Module.Path,
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}, cleanup, nil
}

func unleash(dir, ref, target, out, seed string) ([]byte, error) {
	f, err := os.CreateTemp(out, "gremlins-*.json")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()
	gargs := []string{"unleash", "--output", name,
		"--timeout-coefficient", strconv.Itoa(timeoutCoefficient), "--workers", strconv.Itoa(workers)}
	if ref != "" {
		gargs = append(gargs, "--diff", ref)
	}
	wrapEnv, cleanup, err := goWrapperEnv(dir)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := exec.CommandContext(context.Background(), "gremlins", append(gargs, target)...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "RAPID_SEED="+seed,
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=diff.relative", "GIT_CONFIG_VALUE_0=true",
		"GIT_CONFIG_KEY_1=diff.context", "GIT_CONFIG_VALUE_1=0"), wrapEnv...)
	log, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && (exit.ExitCode() == 10 || exit.ExitCode() == 11) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("gremlins unleash in %s: %w\n%s", dir, err, adapterkit.CapText(log))
	}
	return os.ReadFile(name)
}
