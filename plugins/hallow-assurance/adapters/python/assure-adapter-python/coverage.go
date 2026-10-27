package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
	ievidence "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

type coverageFile struct {
	ExecutedLines []int `json:"executed_lines"`
	MissingLines  []int `json:"missing_lines"`
}

type coverageReport struct {
	Files map[string]coverageFile `json:"files"`
}

func lcovFile(path string, f coverageFile, funcs []pyFunc) ievidence.LCOVFile {
	out := ievidence.LCOVFile{Path: path, Functions: []ievidence.LCOVFunction{}, Lines: map[int]int{}}
	for _, l := range f.MissingLines {
		out.Lines[l] = 0
	}
	for _, l := range f.ExecutedLines {
		out.Lines[l] = 1
	}
	for _, fn := range funcs {
		hits := 0
		for l, h := range out.Lines {
			if h > 0 && l > fn.Start && l <= fn.End {
				hits = 1
				break
			}
		}
		out.Functions = append(out.Functions, ievidence.LCOVFunction{Name: fn.Name, Start: fn.Start, End: fn.End, Hits: hits})
	}
	return out
}

// coverageRC is the whole coverage configuration: an explicit --rcfile keeps an adopter's omit
// settings from hiding a changed file, and source directories make coverage report a changed file
// no test imports instead of leaving it out (which would read as covered).
func coverageRC(files []string) string {
	var dirs []string
	for _, f := range files {
		dirs = append(dirs, filepath.Dir(f))
	}
	slices.Sort(dirs)
	return "[run]\nbranch = False\nsource =\n    " + strings.Join(slices.Compact(dirs), "\n    ") +
		"\n[report]\ninclude =\n    " + strings.Join(files, "\n    ") + "\n"
}

func measure(c runContext, unit, dir string, abs []string) (coverageReport, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return coverageReport{}, err
	}
	rc, data, report := filepath.Join(dir, "coveragerc"), filepath.Join(dir, "coverage.data"), filepath.Join(dir, "coverage.json")
	if err := os.WriteFile(rc, []byte(coverageRC(abs)), 0o644); err != nil {
		return coverageReport{}, err
	}
	python := c.units[unit].python
	args := append([]string{"-m", "coverage", "run", "--rcfile=" + rc, "--data-file=" + data}, pytestArgs(c.seed, filepath.Join(dir, "pytest.xml"))...)
	stdout, stderr, code, err := runToolEnv(unit, []string{"ASSURE_SEED=" + c.seed}, python, args...)
	if err != nil {
		return coverageReport{}, err
	}
	output := append(stdout, stderr...)
	if !slices.Contains([]int{0, 1, 2, 5}, code) {
		return coverageReport{}, fmt.Errorf("coverage run of pytest in %s exited %d: %s", adapterkit.RelSlash(c.top, unit), code, adapterkit.CapText(output))
	}
	_, jout, jcode, err := runToolEnv(unit, nil, python, "-m", "coverage", "json", "--rcfile="+rc, "--data-file="+data, "-o", report)
	if err != nil {
		return coverageReport{}, err
	}
	if jcode != 0 {
		return coverageReport{}, fmt.Errorf("coverage json in %s exited %d: %s\n%s", adapterkit.RelSlash(c.top, unit), jcode, adapterkit.CapText(jout), adapterkit.CapText(output))
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		return coverageReport{}, err
	}
	var r coverageReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return coverageReport{}, fmt.Errorf("coverage json in %s: %w", adapterkit.RelSlash(c.top, unit), err)
	}
	return r, nil
}

func unitCoverage(c runContext, unit, tmp string, i int) ([]ievidence.LCOVFile, error) {
	sources, err := changedSources(c, unit)
	if err != nil || len(sources) == 0 {
		return nil, err
	}
	abs := absFiles(c.root, sources)
	python := c.units[unit].python
	r, err := measure(c, unit, filepath.Join(tmp, strconv.Itoa(i)), abs)
	if err != nil {
		return nil, err
	}
	byPath := map[string]coverageFile{}
	for name, f := range r.Files {
		if !filepath.IsAbs(name) {
			name = filepath.Join(unit, name)
		}
		byPath[relTo(c.root, name)] = f
	}
	info, err := describeFiles(python, unit, abs)
	if err != nil {
		return nil, err
	}
	var out []ievidence.LCOVFile
	for k, s := range sources {
		fi := info[abs[k]]
		if fi.Error != "" {
			return nil, fmt.Errorf("%s does not parse: %s", s, fi.Error)
		}
		f, ok := byPath[s]
		if !ok {
			return nil, fmt.Errorf("coverage in %s has no entry for %s; its lines cannot be shown covered or uncovered", adapterkit.RelSlash(c.top, unit), s)
		}
		out = append(out, lcovFile(s, f, fi.Functions))
	}
	return out, nil
}

func runCoverage(c runContext) ([]evidence, string, error) {
	tmp, err := os.MkdirTemp("", "assure-coverage-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	var files []ievidence.LCOVFile
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		fs, err := unitCoverage(c, unit, tmp, i)
		if err != nil {
			return nil, "", err
		}
		files = append(files, fs...)
	}
	slices.SortFunc(files, func(a, b ievidence.LCOVFile) int { return strings.Compare(a.Path, b.Path) })
	if err := os.WriteFile(filepath.Join(c.out, "coverage.lcov"), ievidence.FormatLCOV(files), 0o644); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "coverage.resolution", Path: "coverage.lcov"}}, "", nil
}
