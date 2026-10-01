package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type budgetFile struct {
	Path         string `json:"path"`
	Role         string `json:"role"`
	AddedCases   *int   `json:"added_cases,omitempty"`
	ChangedLines *int   `json:"changed_lines,omitempty"`
}

type budgetDoc struct {
	Version int          `json:"version"`
	Files   []budgetFile `json:"files"`
}

func isTestingT(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "testing" && sel.Sel.Name == "T"
}

func runSites(fn *ast.FuncDecl) int {
	ts := map[string]bool{}
	addParams := func(ft *ast.FuncType) {
		if ft.Params == nil {
			return
		}
		for _, f := range ft.Params.List {
			if isTestingT(f.Type) {
				for _, n := range f.Names {
					ts[n.Name] = true
				}
			}
		}
	}
	addParams(fn.Type)
	n := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.FuncLit:
			addParams(x.Type)
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" || len(x.Args) != 2 {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && ts[id.Name] {
				n++
			}
		}
		return true
	})
	return n
}

func testCases(src []byte) (map[string]int, error) {
	if src == nil {
		return map[string]int{}, nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		p := testParam(fn)
		if p == "T" && isTestName(fn.Name.Name, "Test") || p == "F" && isTestName(fn.Name.Name, "Fuzz") {
			out[fn.Name.Name] = runSites(fn)
		}
	}
	return out, nil
}

func addedCases(base, head map[string]int) int {
	n := 0
	for name, runs := range head {
		old, ok := base[name]
		if !ok {
			n++
		}
		n += max(0, runs-old)
	}
	return n
}

func baseSource(root, ref, p string) ([]byte, error) {
	listed, err := gitBytes(root, "ls-tree", "--name-only", ref, "--", "./"+p)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(listed)) == 0 {
		return nil, nil
	}
	return gitBytes(root, "show", ref+":./"+p)
}

func headSource(root, p string) ([]byte, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return src, err
}

func countLines(src []byte) int {
	n := bytes.Count(src, []byte("\n"))
	if len(src) > 0 && src[len(src)-1] != '\n' {
		n++
	}
	return n
}

func addedLines(root, ref, p string, tracked bool) (int, error) {
	if !tracked {
		src, err := headSource(root, p)
		return countLines(src), err
	}
	out, err := gitBytes(root, "diff", "--numstat", "--no-ext-diff", "--no-renames", ref, "--", "./"+p)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, nil
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("git diff --numstat %s: unexpected output %q", p, out)
	}
	return n, nil
}

func budgetEntry(root, ref, p, role string, untracked map[string]bool) (budgetFile, error) {
	e := budgetFile{Path: p, Role: role}
	if role == "source" {
		n, err := addedLines(root, ref, p, !untracked[p])
		e.ChangedLines = &n
		return e, err
	}
	var base []byte
	if !untracked[p] {
		var err error
		if base, err = baseSource(root, ref, p); err != nil {
			return e, err
		}
	}
	head, err := headSource(root, p)
	if err != nil {
		return e, err
	}
	b, err := testCases(base)
	if err != nil {
		return e, fmt.Errorf("%s at %s: %w", p, ref, err)
	}
	h, err := testCases(head)
	if err != nil {
		return e, fmt.Errorf("%s: %w", p, err)
	}
	n := addedCases(b, h)
	e.AddedCases = &n
	return e, nil
}

func runBudget(root, ref, out string, _ map[string][]string, _ map[string]string) ([]evidence, error) {
	paths, err := changedPaths(root, ref)
	if err != nil {
		return nil, err
	}
	others, err := gitLines(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	untracked := map[string]bool{}
	for _, p := range others {
		untracked[p] = true
	}
	sort.Strings(paths)
	doc := budgetDoc{Files: []budgetFile{}}
	for _, p := range paths {
		role, err := goRole(root, p)
		if err != nil {
			return nil, err
		}
		if role != "test" && role != "source" {
			continue
		}
		e, err := budgetEntry(root, ref, p, role, untracked)
		if err != nil {
			return nil, err
		}
		doc.Files = append(doc.Files, e)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "test-budget.json"), data, 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "test.budget", Path: "test-budget.json"}}, nil
}
