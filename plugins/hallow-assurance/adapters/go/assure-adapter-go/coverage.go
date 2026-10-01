package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type coverFunc struct {
	name       string
	start, end int
}

type coverFile struct {
	path  string
	funcs []coverFunc
	lines map[int]int
}

type goPackage struct {
	importPath string
	dir        string
	files      []string
}

func listPackages(modDir string, pkgs []string) ([]goPackage, error) {
	args := append([]string{"list", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \" \"}} {{join .CgoFiles \" \"}}"}, pkgs...)
	stdout, stderr, code, err := runTool(modDir, "go", args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("go list in %s: exit %d: %s", modDir, code, capText(stderr))
	}
	var out []goPackage
	for l := range strings.SplitSeq(strings.TrimSpace(string(stdout)), "\n") {
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		out = append(out, goPackage{importPath: parts[0], dir: parts[1], files: strings.Fields(parts[2])})
	}
	return out, nil
}

func receiverName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

func funcSpans(src []byte) ([]coverFunc, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []coverFunc
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			name = receiverName(fn.Recv.List[0].Type) + "." + name
		}
		out = append(out, coverFunc{name: name, start: fset.Position(fn.Pos()).Line, end: fset.Position(fn.End()).Line})
	}
	return out, nil
}

func parseProfile(data []byte) (map[string]map[int]int, error) {
	hits := map[string]map[int]int{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	first := true
	for sc.Scan() {
		l := sc.Text()
		if first {
			first = false
			if !strings.HasPrefix(l, "mode: ") {
				return nil, fmt.Errorf("cover profile: missing mode line")
			}
			continue
		}
		if l == "" {
			continue
		}
		file, sl, el, count, err := profileBlock(l)
		if err != nil {
			return nil, err
		}
		m := hits[file]
		if m == nil {
			m = map[int]int{}
			hits[file] = m
		}
		for line := sl; line <= el; line++ {
			m[line] = max(m[line], count)
		}
	}
	return hits, sc.Err()
}

func profileBlock(l string) (file string, sl, el, count int, err error) {
	bad := fmt.Errorf("cover profile: malformed block %q", l)
	colon := strings.LastIndex(l, ":")
	if colon < 0 {
		return "", 0, 0, 0, bad
	}
	fields := strings.Fields(l[colon+1:])
	if len(fields) != 3 {
		return "", 0, 0, 0, bad
	}
	start, end, ok := strings.Cut(fields[0], ",")
	if !ok {
		return "", 0, 0, 0, bad
	}
	ls, _, _ := strings.Cut(start, ".")
	le, _, _ := strings.Cut(end, ".")
	var errs [3]error
	sl, errs[0] = strconv.Atoi(ls)
	el, errs[1] = strconv.Atoi(le)
	count, errs[2] = strconv.Atoi(fields[2])
	if errors.Join(errs[:]...) != nil || sl < 1 || el < sl || count < 0 {
		return "", 0, 0, 0, bad
	}
	return l[:colon], sl, el, count, nil
}

func convertProfile(root string, pkgs []goPackage, hits map[string]map[int]int) ([]coverFile, error) {
	var out []coverFile
	for _, p := range pkgs {
		dir := p.dir
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("package %s is outside %s", p.importPath, root)
		}
		for _, name := range p.files {
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			funcs, err := funcSpans(src)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
			}
			lines := hits[p.importPath+"/"+name]
			if lines == nil {
				lines = map[int]int{}
			}
			out = append(out, coverFile{path: path.Join(filepath.ToSlash(rel), name), funcs: funcs, lines: lines})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func formatLCOV(files []coverFile) []byte {
	var b bytes.Buffer
	for _, f := range files {
		fmt.Fprintf(&b, "SF:%s\n", f.path)
		for _, fn := range f.funcs {
			fmt.Fprintf(&b, "FN:%d,%d,%s\n", fn.start, fn.end, fn.name)
		}
		for _, fn := range f.funcs {
			hit := 0
			for l := fn.start; l <= fn.end; l++ {
				if f.lines[l] > 0 {
					hit = 1
					break
				}
			}
			fmt.Fprintf(&b, "FNDA:%d,%s\n", hit, fn.name)
		}
		lines := make([]int, 0, len(f.lines))
		for l := range f.lines {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		for _, l := range lines {
			fmt.Fprintf(&b, "DA:%d,%d\n", l, f.lines[l])
		}
		b.WriteString("end_of_record\n")
	}
	return b.Bytes()
}

func moduleCoverage(root, mod, seed, tmp string, pkgs []string) ([]coverFile, error) {
	modDir := filepath.Join(root, mod)
	listed, err := listPackages(modDir, pkgs)
	if err != nil {
		return nil, err
	}
	profile := filepath.Join(tmp, strings.ReplaceAll(path.Clean(mod), "/", "_")+".cover")
	cmd := exec.CommandContext(context.Background(), "go", "test", "-covermode=set", "-coverprofile="+profile,
		"-coverpkg="+strings.Join(pkgs, ","), "./...")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "RAPID_SEED="+seed)
	log, err := cmd.CombinedOutput()
	if _, ok := errors.AsType[*exec.ExitError](err); err != nil && !ok {
		return nil, err
	}
	data, rerr := os.ReadFile(profile)
	if rerr != nil {
		return nil, fmt.Errorf("go test -coverprofile in %s wrote no profile: %w\n%s", modDir, rerr, capText(log))
	}
	hits, err := parseProfile(data)
	if err != nil {
		return nil, err
	}
	return convertProfile(root, listed, hits)
}

func runCoverage(root, _, out string, sel map[string][]string, _ map[string]string) ([]evidence, error) {
	seed, err := rapidSeed(root)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "assure-cover-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	var files []coverFile
	for _, mod := range modules(sel) {
		fs, err := moduleCoverage(root, mod, seed, tmp, sel[mod])
		if err != nil {
			return nil, err
		}
		files = append(files, fs...)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	if err := os.WriteFile(filepath.Join(out, "coverage.lcov"), formatLCOV(files), 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "coverage.resolution", Path: "coverage.lcov"}}, nil
}
