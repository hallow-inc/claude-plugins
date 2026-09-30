package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type Mutant struct {
	Path    string
	Line    int
	Mutator string
	Status  string
}

type strykerReport struct {
	Files map[string]struct {
		Mutants []struct {
			MutatorName string `json:"mutatorName"`
			Status      string `json:"status"`
			Location    struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"location"`
		} `json:"mutants"`
	} `json:"files"`
}

func ParseMutationReport(data []byte) ([]Mutant, error) {
	vs, err := schemas.Validate(schemas.MutationReport, data)
	if err != nil {
		return nil, fmt.Errorf("mutation report: %w", err)
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("mutation report: %s", vs[0])
	}
	var rep strykerReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("mutation report: %w", err)
	}
	paths := make([]string, 0, len(rep.Files))
	for p := range rep.Files {
		if !repoRelative(p) {
			return nil, fmt.Errorf("mutation report: file %q is not a repo-relative path", p)
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := []Mutant{}
	for _, p := range paths {
		for _, m := range rep.Files[p].Mutants {
			out = append(out, Mutant{Path: p, Line: m.Location.Start.Line, Mutator: m.MutatorName, Status: m.Status})
		}
	}
	return out, nil
}

func repoRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
