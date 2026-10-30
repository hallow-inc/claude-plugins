package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

type ScopeFile struct {
	Path   string
	Lang   string
	Role   string
	Ranges [][2]int
}

type UsageError struct {
	Arg    string
	Reason string
}

func (e *UsageError) Error() string { return e.Arg + ": " + e.Reason }

type selection struct {
	arg   string
	paths []string
	rng   *[2]int
}

type scopePath struct {
	rel    string
	inside bool
	dir    bool
}

func ResolveScope(m *core.Manifest, cwd string, args []string) ([]ScopeFile, error) {
	candidates, err := scopeCandidates(m)
	if err != nil {
		return nil, err
	}
	sels := make([]selection, len(args))
	matched := map[string]bool{}
	for i, arg := range args {
		if sels[i], err = selectScope(m, cwd, arg, candidates); err != nil {
			return nil, err
		}
		for _, p := range sels[i].paths {
			matched[p] = true
		}
	}
	claims, err := classifyScope(m, matched)
	if err != nil {
		return nil, err
	}
	return assembleScope(sels, claims)
}

func assembleScope(sels []selection, claims map[string]adapterproto.File) ([]ScopeFile, error) {
	files := map[string]*ScopeFile{}
	whole := map[string]bool{}
	for _, s := range sels {
		hit := false
		for _, p := range s.paths {
			f, ok := claims[p]
			if !ok || (f.Role != "source" && f.Role != "test") {
				continue
			}
			hit = true
			if files[p] == nil {
				files[p] = &ScopeFile{Path: p, Lang: f.Language, Role: f.Role}
			}
			if s.rng == nil {
				whole[p] = true
			} else {
				files[p].Ranges = append(files[p].Ranges, *s.rng)
			}
		}
		if !hit {
			return nil, &UsageError{Arg: s.arg, Reason: "matches no file the manifest classifies as source or test"}
		}
	}
	out := make([]ScopeFile, 0, len(files))
	for _, f := range files {
		if whole[f.Path] {
			f.Ranges = nil
		} else {
			f.Ranges = mergeRanges(f.Ranges)
		}
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b ScopeFile) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func scopeCandidates(m *core.Manifest) (map[string]bool, error) {
	out, err := gitBytes(m.Root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	candidates := map[string]bool{}
	for p := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if p == "" || candidates[p] {
			continue
		}
		ok, err := exists(m.Root, p)
		if err != nil {
			return nil, err
		}
		candidates[p] = ok
	}
	return candidates, nil
}

func classifyScope(m *core.Manifest, matched map[string]bool) (map[string]adapterproto.File, error) {
	claims := map[string]adapterproto.File{}
	if len(matched) == 0 {
		return claims, nil
	}
	paths := make([]string, 0, len(matched))
	for p := range matched {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, lang := range m.Languages {
		files, err := adapterproto.Classify(m.Root, lang, paths)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if !matched[f.Path] {
				return nil, fmt.Errorf("%s classify returned %s, which was not requested", adapterproto.Executable(lang), f.Path)
			}
			if prev, ok := claims[f.Path]; ok && prev.Role != f.Role {
				return nil, fmt.Errorf("%s is ambiguous: %s says %s, %s says %s", f.Path, adapterproto.Executable(prev.Language), prev.Role, adapterproto.Executable(lang), f.Role)
			} else if !ok {
				f.Language = lang
				claims[f.Path] = f
			}
		}
	}
	return claims, nil
}

func selectScope(m *core.Manifest, cwd, arg string, candidates map[string]bool) (selection, error) {
	usage := func(reason string) (selection, error) {
		return selection{}, &UsageError{Arg: arg, Reason: reason}
	}
	if arg == "" {
		return usage("empty scope argument")
	}
	if sp, ok := statScope(m, cwd, arg); ok {
		switch {
		case !sp.inside:
			return usage("is outside the manifest root " + m.Root)
		case !sp.dir && !candidates[sp.rel]:
			return usage("is neither tracked nor an untracked, non-ignored file, so it is not in scope")
		case !sp.dir:
			return selection{arg: arg, paths: []string{sp.rel}}, nil
		}
		return selection{arg: arg, paths: candidatesWhere(candidates, func(p string) bool {
			return sp.rel == "." || strings.HasPrefix(p, sp.rel+"/")
		})}, nil
	}
	if i := strings.LastIndex(arg, ":"); i >= 0 {
		if sp, ok := statScope(m, cwd, arg[:i]); ok {
			rng, reason := parseScopeRange(m, sp, arg[i+1:], candidates)
			if reason != "" {
				return usage(reason)
			}
			return selection{arg: arg, paths: []string{sp.rel}, rng: &rng}, nil
		}
	}
	if strings.ContainsAny(arg, "*?[") {
		g, err := core.CompileGlob(arg)
		if err != nil {
			return usage(err.Error())
		}
		return selection{arg: arg, paths: candidatesWhere(candidates, g.Match)}, nil
	}
	return usage("is not an existing file or directory, a <file>:<start>-<end> range, or a glob")
}

func candidatesWhere(candidates map[string]bool, keep func(string) bool) []string {
	var out []string
	for p, ok := range candidates {
		if ok && keep(p) {
			out = append(out, p)
		}
	}
	return out
}

func statScope(m *core.Manifest, cwd, arg string) (scopePath, bool) {
	if strings.ContainsRune(arg, 0) {
		return scopePath{}, false
	}
	abs := arg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return scopePath{}, false
	}
	rel, inside := m.Rel(abs)
	return scopePath{rel: rel, inside: inside, dir: info.IsDir()}, true
}

func parseScopeRange(m *core.Manifest, sp scopePath, spec string, candidates map[string]bool) (rng [2]int, reason string) {
	s, e, ok := strings.Cut(spec, "-")
	start, okS := decimal(s)
	end, okE := decimal(e)
	switch {
	case !ok || !okS || !okE:
		return rng, "range must be <file>:<start>-<end> with decimal line numbers"
	case start < 1 || start > end:
		return rng, "range must satisfy 1 <= start <= end"
	case !sp.inside:
		return rng, "is outside the manifest root " + m.Root
	case sp.dir:
		return rng, "a line range needs a file, not a directory"
	case !candidates[sp.rel]:
		return rng, "matches no file the manifest classifies as source or test"
	}
	n, err := lineCount(filepath.Join(m.Root, filepath.FromSlash(sp.rel)))
	if err != nil {
		return rng, err.Error()
	}
	if end > n {
		return rng, fmt.Sprintf("range ends past line %d, the last line of %s", n, sp.rel)
	}
	return [2]int{start, end}, ""
}

func decimal(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func mergeRanges(rs [][2]int) [][2]int {
	slices.SortFunc(rs, func(a, b [2]int) int { return a[0] - b[0] })
	var out [][2]int
	for _, r := range rs {
		if last := len(out) - 1; last >= 0 && r[0] <= out[last][1]+1 {
			out[last][1] = max(out[last][1], r[1])
			continue
		}
		out = append(out, r)
	}
	return out
}
