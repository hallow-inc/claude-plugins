package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func isTestName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return len(name) == len(prefix) || !unicode.IsLower(r)
}

func testParam(fn *ast.FuncDecl) string {
	if fn.Recv != nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) > 1 {
		return ""
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "testing" {
		return ""
	}
	return sel.Sel.Name
}

func testFuncs(src []byte) (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		p := testParam(fn)
		if isTest := p == "T" && isTestName(name, "Test") || p == "F" && isTestName(name, "Fuzz"); !isTest {
			continue
		}
		out[name] = string(src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset])
	}
	return out, nil
}

func baseTestFuncs(root, ref, dir string) (map[string]string, error) {
	names, err := gitBytes(root, "ls-tree", "--name-only", ref, "--", "./"+path.Clean(dir)+"/")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for n := range strings.SplitSeq(strings.TrimSpace(string(names)), "\n") {
		if !strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := gitBytes(root, "show", ref+":./"+path.Join(dir, path.Base(n)))
		if err != nil {
			return nil, err
		}
		fns, err := testFuncs(src)
		if err != nil {
			continue
		}
		for k, v := range fns {
			out[k] = v
		}
	}
	return out, nil
}

func selectTests(root, ref string, changedTests []string) (map[string][]string, error) {
	byDir := map[string][]string{}
	for _, p := range changedTests {
		byDir[path.Dir(p)] = append(byDir[path.Dir(p)], p)
	}
	sel := map[string][]string{}
	for dir, files := range byDir {
		base, err := baseTestFuncs(root, ref, dir)
		if err != nil {
			return nil, err
		}
		for _, p := range files {
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			fns, err := testFuncs(src)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			sel[dir] = append(sel[dir], changedFuncs(base, fns)...)
		}
		sort.Strings(sel[dir])
	}
	return sel, nil
}

func changedFuncs(base, head map[string]string) []string {
	var out []string
	for name, body := range head {
		if old, ok := base[name]; !ok || old != body {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func extractBase(root, ref, mod, dst string) error {
	tree := ref + ":./"
	if mod != "." {
		tree += mod
	}
	data, err := gitBytes(root, "archive", "--format=tar", tree)
	if err != nil {
		return err
	}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("git archive: unsafe path %q", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFrom(target, tr, h.Size); err != nil {
				return err
			}
		}
	}
}

func writeFrom(target string, r io.Reader, size int64) error {
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, r, size)
	return errors.Join(err, f.Close())
}

func overlayTests(root, mod, dst string, changedTests []string) error {
	for _, p := range changedTests {
		rel, ok := p, true
		if mod != "." {
			rel, ok = strings.CutPrefix(p, mod+"/")
		}
		if !ok {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		case err != nil:
			return err
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, src, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func runFailOnBase(root, ref, out string, sel map[string][]string, versions map[string]string) ([]evidence, error) {
	paths, err := changedPaths(root, ref)
	if err != nil {
		return nil, err
	}
	var changedTests []string
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") && !ignoredByGo(p) {
			changedTests = append(changedTests, p)
		}
	}
	selected, err := selectTests(root, ref, changedTests)
	if err != nil {
		return nil, err
	}
	seed, err := rapidSeed(root)
	if err != nil {
		return nil, err
	}
	suites := xmlSuites{Suites: []xmlSuite{}}
	for _, mod := range modules(sel) {
		dirs := selectedIn(root, mod, selected)
		if len(dirs) == 0 {
			continue
		}
		s, err := baseSuites(root, ref, mod, seed, changedTests, dirs)
		if err != nil {
			return nil, err
		}
		suites.Suites = append(suites.Suites, s...)
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "fail-on-base.xml"), append([]byte(xml.Header), data...), 0o644); err != nil {
		return nil, err
	}
	return []evidence{{Type: "test.fail_on_base", Path: "fail-on-base.xml"}}, nil
}

func selectedIn(root, mod string, selected map[string][]string) map[string][]string {
	dirs := map[string][]string{}
	for dir, names := range selected {
		if m, ok := moduleOf(root, dir); ok && m == mod && len(names) > 0 {
			dirs[dir] = names
		}
	}
	return dirs
}

func baseSuites(root, ref, mod, seed string, changedTests []string, dirs map[string][]string) ([]xmlSuite, error) {
	tmp, err := os.MkdirTemp("", "assure-fail-on-base-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := extractBase(root, ref, mod, tmp); err != nil {
		return nil, err
	}
	if err := overlayTests(root, mod, tmp, changedTests); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(dirs))
	for d := range dirs {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	var suites []xmlSuite
	for _, dir := range keys {
		names := dirs[dir]
		rel := "./" + path.Clean(strings.TrimPrefix(strings.TrimPrefix(dir, mod), "/"))
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = regexp.QuoteMeta(n)
		}
		cmd := exec.CommandContext(context.Background(), "go", "test", "-json", "-count=1", "-run", "^("+strings.Join(quoted, "|")+")$", rel)
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(), "RAPID_SEED="+seed)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return nil, err
			}
		}
		suites = append(suites, topLevelSuite(dir, names, parseEvents(stdout.Bytes()), stderr.String()))
	}
	return suites, nil
}

func topLevelSuite(dir string, names []string, events []event, stderr string) xmlSuite {
	result := map[string]string{}
	var pkgOut strings.Builder
	for _, e := range events {
		if e.Test == "" || strings.Contains(e.Test, "/") {
			if e.Test == "" && e.Action == "output" || strings.HasPrefix(e.Action, "build-") {
				appendCapped(&pkgOut, e.Output)
			}
			continue
		}
		switch e.Action {
		case "pass", "fail", "skip":
			result[e.Test] = e.Action
		}
	}
	appendCapped(&pkgOut, stderr)
	s := xmlSuite{Name: dir, Cases: []xmlCase{}}
	for _, n := range names {
		c := xmlCase{Name: n, Classname: dir}
		switch result[n] {
		case "fail":
			c.Failure = &xmlText{Message: "fails on the base commit"}
		case "skip":
			c.Skipped = &struct{}{}
		case "pass":
		default:
			c.Error = &xmlText{Message: "did not run on the base commit", Body: capText([]byte(pkgOut.String()))}
		}
		s.Cases = append(s.Cases, c)
	}
	s.Tests = len(s.Cases)
	return s
}
