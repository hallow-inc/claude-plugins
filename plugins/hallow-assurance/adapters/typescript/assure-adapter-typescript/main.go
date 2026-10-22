package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

//go:embed reference.md
var reference []byte

type describe struct {
	Protocol   int                 `json:"protocol"`
	Languages  []string            `json:"languages"`
	Claims     []string            `json:"claims"`
	Patterns   map[string][]string `json:"patterns"`
	Objectives map[string]any      `json:"objectives"`
	Reference  string              `json:"reference"`
}

var sourceExts = []string{"ts", "tsx", "mts", "cts"}

func globsFor(format string, exts []string) []string {
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		out = append(out, fmt.Sprintf(format, e))
	}
	return out
}

var configExts = []string{"ts", "mts", "cts", "js", "mjs", "cjs"}

var description = describe{
	Protocol:  2,
	Reference: "typescript",
	Languages: []string{"typescript"},
	Claims:    globsFor("**/*.%s", sourceExts),
	Patterns: map[string][]string{
		"generated": {"**/*.d.ts", "**/*.d.mts", "**/*.d.cts"},
		"test":      append(globsFor("**/*.test.%s", sourceExts), globsFor("**/*.spec.%s", sourceExts)...),
		"config": append(append([]string{
			"**/package.json", "**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock",
			"**/tsconfig.json", "**/tsconfig.*.json",
		}, globsFor("**/vitest.config.%s", configExts)...), globsFor("**/eslint.config.%s", configExts)...),
	},
	Objectives: map[string]any{
		"VER-TESTS-PASS":          map[string]any{"tool": "vitest", "fast": true},
		"VER-MUTATION-CHANGED":    map[string]any{"tool": "stryker (vitest runner)"},
		"VER-FAIL-ON-BASE":        map[string]any{"tool": "vitest"},
		"VER-ROBUST-FUZZ":         map[string]any{"tool": "vitest, fast-check"},
		"VER-COVERAGE-RESOLUTION": map[string]any{"tool": "vitest, @vitest/coverage-v8"},
	},
}

var roles = mustRoles()

func mustRoles() core.Roles {
	r, err := core.NewRoles(map[string]core.RolePatterns{
		"typescript": {Claims: description.Claims, Patterns: description.Patterns},
	})
	if err != nil {
		panic(err)
	}
	return r
}

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
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-typescript describe | reference | classify <path>... | tools --root <dir> | run <objective> --changed-from <ref> --out <dir>")
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
				_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: %v\n", err)
				return 1
			}
			if claimed {
				files = append(files, file{Path: p, Language: "typescript", Role: role})
			}
		}
		out = map[string]any{"protocol": 0, "files": files}
	case "tools":
		return toolsOp(args[1:], stdout, stderr)
	case "run":
		return runObjective(args[1:], stdout, stderr)
	case "reference":
		if _, err := stdout.Write(reference); err != nil {
			_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: %v\n", err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: unknown subcommand %q\n", args[0])
		return 2
	}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-typescript: %v\n", err)
		return 1
	}
	return 0
}

func hasSourceExt(p string) bool {
	for _, e := range sourceExts {
		if strings.HasSuffix(p, "."+e) {
			return true
		}
	}
	return false
}

func classify(p string) (string, bool, error) {
	role, err := roles.Of(p)
	if err != nil {
		return "", false, err
	}
	if role == core.Unclassified {
		return "", false, nil
	}
	if !hasSourceExt(p) {
		return string(role), true, nil
	}
	gen, err := hasGeneratedHeader(p)
	if err != nil {
		return "", false, err
	}
	if gen {
		return string(core.Generated), true, nil
	}
	return string(role), true, nil
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
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		chunk, long, err := r.ReadLine()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("%s: %w", p, err)
		}
		line := string(chunk)
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			return false, nil
		}
		if !long && generatedHeader.MatchString(line) {
			return true, nil
		}
		for long {
			_, long, err = r.ReadLine()
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("%s: %w", p, err)
			}
		}
	}
}
