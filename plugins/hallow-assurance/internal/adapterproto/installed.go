package adapterproto

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var languageID = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func Installed() []string {
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			lang, ok := strings.CutPrefix(e.Name(), Executable(""))
			if !ok || seen[lang] || !languageID.MatchString(lang) {
				continue
			}
			if _, err := Resolve(lang); err == nil {
				seen[lang] = true
			}
		}
	}
	return slices.Sorted(func(yield func(string) bool) {
		for l := range seen {
			if !yield(l) {
				return
			}
		}
	})
}
