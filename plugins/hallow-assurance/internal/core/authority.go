package core

import (
	"fmt"
	"slices"
)

type Authority string

const (
	AuthorityLocal Authority = "local"
	AuthorityCI    Authority = "ci"
)

func UntrackedPinProblems(a Authority, untracked []string, pinnedBy map[string]string) []string {
	if a != AuthorityCI || len(untracked) == 0 {
		return nil
	}
	var out []string
	for _, tool := range slices.Sorted(slices.Values(untracked)) {
		out = append(out, fmt.Sprintf("%s is pinned by %s, which git does not track, so CI cannot see the pin; commit the pin or remove it", tool, pinnedBy[tool]))
	}
	return out
}
