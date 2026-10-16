package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type lintPack struct {
	name  string
	rules map[string]any
}

const fetchCall = "CallExpression:matches([callee.type='Identifier'][callee.name='fetch'], [callee.type='MemberExpression'][callee.computed=false][callee.object.type='Identifier'][callee.object.name=/^(globalThis|window|self)$/][callee.property.name='fetch'])"

func restricted(selectors ...[2]string) []any {
	out := []any{"error"}
	for _, s := range selectors {
		out = append(out, map[string]string{"selector": s[0], "message": s[1]})
	}
	return out
}

var (
	checkReturnsPack = lintPack{"check-returns/v1", map[string]any{
		"@typescript-eslint/no-floating-promises": "error",
		"@typescript-eslint/no-misused-promises":  "error",
	}}
	complexityPack = lintPack{"complexity/v1", map[string]any{
		"complexity": []any{"error", map[string]int{"max": 0}},
	}}
	noUnsafePack = lintPack{"no-unsafe/v1", map[string]any{
		"no-eval":                            "error",
		"@typescript-eslint/no-implied-eval": "error",
		"no-new-func":                        "error",
		"@typescript-eslint/no-explicit-any": "error",
		"@typescript-eslint/ban-ts-comment":  "error",
		"no-restricted-syntax": restricted(
			[2]string{"TSAsExpression[expression.type='TSAsExpression'][expression.typeAnnotation.type='TSUnknownKeyword']", "double-cast: an `as unknown as` cast bypasses type checking"},
			[2]string{"MemberExpression[object.type='Identifier'][object.name='Reflect']", "reflect: Reflect bypasses static typing"},
			[2]string{"NewExpression[callee.type='Identifier'][callee.name='Proxy']", "proxy: a Proxy changes property access at run time"},
		),
	}}
	resourceBoundsPack = lintPack{"resource-bounds/v1", map[string]any{
		"no-constant-condition": []any{"error", map[string]string{"checkLoops": "all"}},
		"no-restricted-syntax": restricted(
			[2]string{fetchCall + "[arguments.length<2]", "fetch-signal: fetch without an AbortSignal can wait forever"},
			[2]string{fetchCall + " > ObjectExpression:nth-child(2):not(:has(> Property[key.name='signal'], > Property[key.value='signal']))", "fetch-signal: fetch without an AbortSignal can wait forever"},
		),
	}}
)

const packConfig = `import { createRequire } from "node:module";
const tseslint = createRequire(%s)("typescript-eslint");
export default [{
  files: %s,
  linterOptions: { noInlineConfig: true },
  languageOptions: { parser: tseslint.parser, parserOptions: { projectService: true, tsconfigRootDir: %s } },
  plugins: { "@typescript-eslint": tseslint.plugin },
  rules: %s,
}];
`

func (p lintPack) config(unit string) ([]byte, error) {
	files, err := json.Marshal(globsFor("**/*.%s", sourceExts))
	if err != nil {
		return nil, err
	}
	rules, err := json.Marshal(p.rules)
	if err != nil {
		return nil, err
	}
	pkg, err := json.Marshal(filepath.Join(unit, "package.json"))
	if err != nil {
		return nil, err
	}
	root, err := json.Marshal(unit)
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, packConfig, pkg, files, root, rules), nil
}

func sourceFiles(files []string) ([]string, error) {
	var out []string
	for _, f := range files {
		role, _, err := classify(f)
		if err != nil {
			return nil, err
		}
		if role == "source" {
			out = append(out, f)
		}
	}
	return out, nil
}

func (p lintPack) run(c runContext) ([]evidence, string, error) {
	tmp, err := os.MkdirTemp("", "assure-eslint-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	r := newRun("eslint " + p.name)
	for i, unit := range slices.Sorted(maps.Keys(c.sel)) {
		files, err := sourceFiles(c.sel[unit])
		if err != nil {
			return nil, "", err
		}
		if len(files) == 0 {
			continue
		}
		if !exists(filepath.Join(unit, "tsconfig.json")) {
			return nil, fmt.Sprintf("%s is missing; the ESLint rule packs take type information from each unit's tsconfig.json", relSlash(c.top, filepath.Join(unit, "tsconfig.json"))), nil
		}
		cfg, err := p.config(unit)
		if err != nil {
			return nil, "", err
		}
		path := filepath.Join(tmp, fmt.Sprintf("eslint-%d.config.mjs", i))
		if err := os.WriteFile(path, cfg, 0o644); err != nil {
			return nil, "", err
		}
		args := append([]string{"--config", path, "--format", "json", "--no-warn-ignored"}, absFiles(c.root, files)...)
		stdout, stderr, code, err := runTool(unit, c.tools[unit]["eslint"].bin, args...)
		if err != nil {
			return nil, "", err
		}
		r.Results = append(r.Results, p.results(c.root, relSlash(c.top, unit), files, stdout, stderr, code == 0 || code == 1)...)
	}
	ev, err := writeSARIF(c.out, r)
	return ev, "", err
}

var complexityMessage = regexp.MustCompile(`^(.+) has a complexity of (\d+)\.`)

func complexityOf(msg string) (string, int, bool) {
	sm := complexityMessage.FindStringSubmatch(msg)
	if sm == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(sm[2])
	return sm[1], n, err == nil
}

func (p lintPack) result(uri string, m eslintMessage) (sarifResult, bool) {
	if m.Fatal {
		return result("parse", "error", m.Message, uri), true
	}
	if m.RuleID == nil {
		return sarifResult{}, false
	}
	rule := *m.RuleID
	if rule == "complexity" {
		subject, n, ok := complexityOf(m.Message)
		if !ok {
			return result("complexity", "error", "unrecognized complexity message: "+m.Message, uri), true
		}
		res := result("cyclomatic", "note", "cyclomatic complexity of "+subject, uri)
		res.Properties = &sarifProperties{Metric: &n}
		return res, true
	}
	if rule == "no-restricted-syntax" {
		id, _, _ := strings.Cut(m.Message, ": ")
		return result(id, "error", m.Message, uri), true
	}
	if p.rules[rule] != nil {
		return eslintResult(uri, m), true
	}
	return sarifResult{}, false
}

func (p lintPack) results(root, unit string, files []string, stdout, stderr []byte, ok bool) []sarifResult {
	var reported []eslintFile
	if !ok || json.Unmarshal(stdout, &reported) != nil {
		return []sarifResult{result("eslint", "error", "eslint failed in "+unit+": "+capText(stderr), "")}
	}
	out := []sarifResult{}
	seen := map[string]bool{}
	for _, f := range reported {
		uri := relTo(root, f.FilePath)
		seen[uri] = true
		for _, m := range f.Messages {
			if res, keep := p.result(uri, m); keep {
				out = append(out, res)
			}
		}
	}
	for _, f := range files {
		if !seen[f] {
			out = append(out, result("eslint", "error", "eslint did not lint this file with the "+p.name+" pack", f))
		}
	}
	return out
}
