package main

import (
	"cmp"
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

type istanbulRange struct {
	Start struct {
		Line int `json:"line"`
	} `json:"start"`
	End struct {
		Line int `json:"line"`
	} `json:"end"`
}

type istanbulFunc struct {
	Name string        `json:"name"`
	Decl istanbulRange `json:"decl"`
	Loc  istanbulRange `json:"loc"`
}

type istanbulFile struct {
	StatementMap map[string]istanbulRange `json:"statementMap"`
	FnMap        map[string]istanbulFunc  `json:"fnMap"`
	S            map[string]int           `json:"s"`
	F            map[string]int           `json:"f"`
}

func numericKeys[V any](m map[string]V) ([]string, error) {
	keys := slices.Collect(maps.Keys(m))
	nums := map[string]int{}
	for _, k := range keys {
		n, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("istanbul key %q is not a number", k)
		}
		nums[k] = n
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Compare(nums[a], nums[b]) })
	return keys, nil
}

// Counts vary with property-test input counts, so only hit or not is kept, as go test -covermode=set does.
func hit(count int) int {
	return min(count, 1)
}

func lcovFile(path string, f istanbulFile) (ievidence.LCOVFile, error) {
	out := ievidence.LCOVFile{Path: path, Functions: []ievidence.LCOVFunction{}, Lines: map[int]int{}}
	for id, st := range f.StatementMap {
		l := st.Start.Line
		if l < 1 {
			return ievidence.LCOVFile{}, fmt.Errorf("%s: statement %s starts on line %d", path, id, l)
		}
		out.Lines[l] = max(out.Lines[l], hit(f.S[id]))
	}
	ids, err := numericKeys(f.FnMap)
	if err != nil {
		return ievidence.LCOVFile{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, id := range ids {
		fn := f.FnMap[id]
		start, end := fn.Decl.Start.Line, fn.Loc.End.Line
		if start < 1 || end < start {
			return ievidence.LCOVFile{}, fmt.Errorf("%s: function %q spans lines %d to %d", path, fn.Name, start, end)
		}
		out.Functions = append(out.Functions, ievidence.LCOVFunction{Name: fn.Name, Start: start, End: end, Hits: hit(f.F[id])})
	}
	return out, nil
}

var globSpecial = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`, `(`, `\(`, `)`, `\)`, `!`, `\!`, `+`, `\+`, `@`, `\@`)

func unitCoverage(c runContext, unit, seed, tmp string, i int) ([]ievidence.LCOVFile, error) {
	sources, err := changedSources(c, unit)
	if err != nil || len(sources) == 0 {
		return nil, err
	}
	dir := filepath.Join(tmp, strconv.Itoa(i))
	args := []string{"run", "--passWithNoTests", "--coverage.enabled", "--coverage.provider=v8", "--coverage.reporter=json", "--coverage.reportOnFailure", "--coverage.reportsDirectory=" + dir}
	for _, s := range sources {
		args = append(args, "--coverage.include="+globSpecial.Replace(relSlash(unit, filepath.Join(c.root, filepath.FromSlash(s)))))
	}
	stdout, stderr, _, err := runToolEnv(unit, []string{"ASSURE_SEED=" + seed}, c.tools[unit]["vitest"].bin, args...)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "coverage-final.json"))
	if err != nil {
		return nil, fmt.Errorf("vitest coverage in %s wrote no report: %w\n%s", relSlash(c.top, unit), err, adapterkit.CapText(append(stdout, stderr...)))
	}
	var report map[string]istanbulFile
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("vitest coverage in %s: %w", relSlash(c.top, unit), err)
	}
	byPath := map[string]istanbulFile{}
	for name, f := range report {
		byPath[relTo(c.root, name)] = f
	}
	var out []ievidence.LCOVFile
	for _, s := range sources {
		f, ok := byPath[s]
		if !ok {
			return nil, fmt.Errorf("vitest coverage in %s has no entry for %s", relSlash(c.top, unit), s)
		}
		lf, err := lcovFile(s, f)
		if err != nil {
			return nil, err
		}
		out = append(out, lf)
	}
	return out, nil
}

func runCoverage(c runContext) ([]evidence, string, error) {
	seed, err := assureSeed(c.root)
	if err != nil {
		return nil, "", err
	}
	tmp, err := os.MkdirTemp("", "assure-coverage-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	var files []ievidence.LCOVFile
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		fs, err := unitCoverage(c, unit, seed, tmp, i)
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
