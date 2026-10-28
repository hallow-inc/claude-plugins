package adapterkit

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type ScopeEntry struct {
	Path   string   `json:"path"`
	Ranges [][2]int `json:"ranges"`
}

type Scope []ScopeEntry

type RunArgs struct {
	Objective   string
	Out         string
	ChangedFrom string
	Scope       Scope
}

func ParseRun(args []string) (RunArgs, error) {
	if len(args) == 0 {
		return RunArgs{}, errors.New("missing objective")
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	ref := fs.String("changed-from", "", "git ref to diff against")
	scope := fs.String("scope", "", "scope file naming the paths and line ranges to run against")
	out := fs.String("out", "", "directory for evidence files")
	if err := fs.Parse(args[1:]); err != nil {
		return RunArgs{}, err
	}
	switch {
	case fs.NArg() > 0:
		return RunArgs{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	case *out == "":
		return RunArgs{}, errors.New("missing --out")
	case *ref != "" && *scope != "":
		return RunArgs{}, errors.New("--changed-from and --scope are mutually exclusive")
	case *ref == "" && *scope == "":
		return RunArgs{}, errors.New("one of --changed-from or --scope is required")
	}
	ra := RunArgs{Objective: args[0], Out: *out, ChangedFrom: *ref}
	if *scope != "" {
		s, err := ReadScope(*scope)
		if err != nil {
			return RunArgs{}, err
		}
		ra.Scope = s
	}
	return ra, nil
}

func ReadScope(file string) (Scope, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	vs, err := schemas.Validate(schemas.AdapterScope, data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(vs) > 0 {
		lines := make([]string, len(vs))
		for i, v := range vs {
			lines[i] = file + ": " + v.String()
		}
		return nil, errors.New(strings.Join(lines, "\n"))
	}
	var s Scope
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	for _, e := range s {
		for _, r := range e.Ranges {
			if r[0] > r[1] {
				return nil, fmt.Errorf("%s: %s: range [%d, %d] starts after it ends", file, e.Path, r[0], r[1])
			}
		}
	}
	return s, nil
}

func (s Scope) Paths() []string {
	var paths []string
	for _, e := range s {
		if !slices.Contains(paths, e.Path) {
			paths = append(paths, e.Path)
		}
	}
	return paths
}

func (s Scope) Contains(path string, line int) bool {
	for _, e := range s {
		if e.Path != path {
			continue
		}
		if len(e.Ranges) == 0 {
			return true
		}
		for _, r := range e.Ranges {
			if r[0] <= line && line <= r[1] {
				return true
			}
		}
	}
	return false
}
