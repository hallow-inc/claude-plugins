package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func unitOf(top, dir string) (string, bool) {
	for {
		if exists(filepath.Join(dir, "package.json")) {
			return dir, true
		}
		if dir == top {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir || !strings.HasPrefix(dir, top+string(filepath.Separator)) {
			return "", false
		}
		dir = parent
	}
}

var unsupportedLayouts = []struct{ name, layout string }{
	{".pnp.cjs", "Yarn Plug'n'Play"},
	{"deno.json", "Deno"},
	{"deno.jsonc", "Deno"},
}

func refusal(top, unit string) string {
	for dir := unit; ; dir = filepath.Dir(dir) {
		for _, u := range unsupportedLayouts {
			p := filepath.Join(dir, u.name)
			if exists(p) {
				rel, _ := filepath.Rel(top, p)
				return fmt.Sprintf("%s: the %s layout is unsupported; assure-adapter-typescript runs only on node_modules installs", filepath.ToSlash(rel), u.layout)
			}
		}
		if dir == top || filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func inNodeModules(rel string) bool {
	return slices.Contains(strings.Split(rel, "/"), "node_modules")
}

func unitFiles(root, top, unit string) ([]string, error) {
	listed, err := adapterkit.GitPaths(unit, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range listed {
		abs := filepath.Join(unit, filepath.FromSlash(l))
		if inNodeModules(l) || !exists(abs) {
			continue
		}
		if u, ok := unitOf(top, filepath.Dir(abs)); !ok || u != unit {
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") {
			continue
		}
		role, claimed, err := classify(rel)
		if err != nil {
			return nil, err
		}
		if claimed && (role == "source" || role == "test") {
			out = append(out, rel)
		}
	}
	return out, nil
}

func selectUnits(root, top string, paths []string) (map[string][]string, string, error) {
	sel := map[string][]string{}
	whole := map[string]bool{}
	for _, p := range paths {
		role, claimed, err := classify(p)
		if err != nil {
			return nil, "", err
		}
		if !claimed {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(p))
		unit, ok := unitOf(top, filepath.Dir(abs))
		if !ok {
			return nil, fmt.Sprintf("%s has no package.json in its directory or any parent up to the git top-level; assure-adapter-typescript needs one to find its unit", p), nil
		}
		if _, seen := sel[unit]; !seen {
			sel[unit] = []string{}
		}
		if role == "config" {
			whole[unit] = true
		} else if (role == "source" || role == "test") && exists(abs) {
			sel[unit] = append(sel[unit], p)
		}
	}
	for _, unit := range slices.Sorted(maps.Keys(sel)) {
		if msg := refusal(top, unit); msg != "" {
			return nil, msg, nil
		}
	}
	for unit := range whole {
		files, err := unitFiles(root, top, unit)
		if err != nil {
			return nil, "", err
		}
		sel[unit] = files
	}
	for unit := range sel {
		slices.Sort(sel[unit])
		sel[unit] = slices.Compact(sel[unit])
	}
	return sel, "", nil
}
