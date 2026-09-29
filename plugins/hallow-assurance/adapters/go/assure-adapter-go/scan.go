package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fzipp/gocyclo"
)

func packageDirs(root, mod string, pkgs []string) ([]string, error) {
	var dirs []string
	for _, p := range pkgs {
		if p != "./..." {
			dirs = append(dirs, path.Join(mod, p))
			continue
		}
		base := filepath.Join(root, mod)
		err := filepath.WalkDir(base, func(full string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if full != base {
				if ignoredByGo(rel + "/x.go") {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(full, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			if hasGoFiles(full) {
				dirs = append(dirs, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func sourceFiles(root string, sel map[string][]string) ([]string, error) {
	var out []string
	for _, mod := range modules(sel) {
		dirs, err := packageDirs(root, mod, sel[mod])
		if err != nil {
			return nil, err
		}
		for _, dir := range dirs {
			matches, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
			if err != nil {
				return nil, err
			}
			for _, m := range matches {
				rel := path.Join(dir, filepath.Base(m))
				if strings.HasSuffix(rel, "_test.go") {
					continue
				}
				gen, err := hasGeneratedHeader(m)
				if err != nil {
					return nil, err
				}
				if !gen {
					out = append(out, path.Clean(rel))
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func parseSource(root string, sel map[string][]string, mode parser.Mode, each func(rel string, fset *token.FileSet, f *ast.File)) error {
	files, err := sourceFiles(root, sel)
	if err != nil {
		return err
	}
	for _, rel := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, mode)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		each(rel, fset, f)
	}
	return nil
}

func runComplexity(root, _, out string, sel map[string][]string, _ map[string]string) ([]evidence, error) {
	r := newRun("gocyclo")
	err := parseSource(root, sel, 0, func(rel string, fset *token.FileSet, f *ast.File) {
		for _, s := range gocyclo.AnalyzeASTFile(f, fset, nil) {
			res := result("cyclomatic", "note", "cyclomatic complexity of "+s.PkgName+"."+s.FuncName, rel)
			res.Properties = &sarifProperties{Metric: &s.Complexity}
			r.Results = append(r.Results, res)
		}
	})
	if err != nil {
		return nil, err
	}
	return writeSARIF(out, r)
}

func runNoUnsafe(root, _, out string, sel map[string][]string, _ map[string]string) ([]evidence, error) {
	r := newRun("assure-adapter-go no-unsafe/v1")
	err := parseSource(root, sel, parser.ParseComments, func(rel string, _ *token.FileSet, f *ast.File) {
		for _, imp := range f.Imports {
			switch p, _ := strconv.Unquote(imp.Path.Value); p {
			case "unsafe", "reflect":
				r.Results = append(r.Results, result("import-"+p, "error", "imports "+p, rel))
			}
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.HasPrefix(c.Text, "//go:linkname ") {
					r.Results = append(r.Results, result("linkname", "error", strings.TrimPrefix(c.Text, "//"), rel))
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return writeSARIF(out, r)
}
