package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

type describe struct {
	Protocol   int                 `json:"protocol"`
	Languages  []string            `json:"languages"`
	Claims     []string            `json:"claims"`
	Patterns   map[string][]string `json:"patterns"`
	Objectives map[string]any      `json:"objectives"`
}

var description = describe{
	Languages: []string{"go"},
	Claims:    []string{"**/*.go"},
	Patterns: map[string][]string{
		"test":        {"**/*_test.go"},
		"fuzz_corpus": {"**/testdata/fuzz/**"},
		"config":      {"**/go.mod", "**/go.sum", "**/go.work", "**/go.work.sum"},
	},
	Objectives: map[string]any{
		"VER-TESTS-PASS":       map[string]any{"tool": "go test", "fast": true},
		"CODE-ZERO-WARNINGS":   map[string]any{"tool": "golangci-lint", "fast": true},
		"CODE-CHECK-RETURNS":   map[string]any{"tool": "golangci-lint check-returns/v1 (errcheck)"},
		"CODE-RESOURCE-BOUNDS": map[string]any{"tool": "golangci-lint resource-bounds/v1 (bodyclose, noctx, gosec G110 G112 G114)"},
		"CODE-COMPLEXITY":      map[string]any{"tool": "gocyclo"},
		"CODE-NO-UNSAFE":       map[string]any{"tool": "assure-adapter-go no-unsafe/v1"},
		"VER-MUTATION-CHANGED": map[string]any{"tool": "gremlins"},
		"VER-FAIL-ON-BASE":     map[string]any{"tool": "go test on the base commit"},
	},
}

var configFiles = map[string]bool{"go.mod": true, "go.sum": true, "go.work": true, "go.work.sum": true}

type file struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Role     string `json:"role"`
}

var generatedHeader = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-go describe | classify <path>... | run <objective> --changed-from <ref> --out <dir>")
		return 2
	}
	var out any
	switch args[0] {
	case "describe":
		out = description
	case "classify":
		files := []file{}
		for _, p := range args[1:] {
			role, claimed, err := classify(p)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "assure-adapter-go: %v\n", err)
				return 1
			}
			if claimed {
				files = append(files, file{Path: p, Language: "go", Role: role})
			}
		}
		out = map[string]any{"protocol": 0, "files": files}
	case "run":
		return runObjective(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "assure-adapter-go: unknown subcommand %q\n", args[0])
		return 2
	}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-go: %v\n", err)
		return 1
	}
	return 0
}

func globRole(p string) (string, bool) {
	segs := strings.Split(p, "/")
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == "testdata" && segs[i+1] == "fuzz" {
			return "fuzz_corpus", true
		}
	}
	base := path.Base(p)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return "test", true
	case configFiles[base]:
		return "config", true
	case strings.HasSuffix(base, ".go"):
		return "source", true
	}
	return "", false
}

func classify(p string) (string, bool, error) {
	role, claimed := globRole(p)
	if !claimed || !strings.HasSuffix(p, ".go") {
		return role, claimed, nil
	}
	gen, err := hasGeneratedHeader(p)
	if err != nil {
		return "", false, err
	}
	if gen {
		return "generated", true, nil
	}
	return role, true, nil
}

func hasGeneratedHeader(p string) (bool, error) {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || info.IsDir() {
		return false, err
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "package ") {
			return false, nil
		}
		if generatedHeader.MatchString(line) {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("%s: %w", p, err)
	}
	return false, nil
}
