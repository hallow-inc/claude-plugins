package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const ResolutionsFile = ".assure/coverage-resolutions.yaml"

type Resolution struct {
	Path       string `json:"path"`
	Function   string `json:"function"`
	Resolution string `json:"resolution"`
	Rationale  string `json:"rationale"`
	Approver   string `json:"approver"`
}

func LoadResolutions(root string) ([]Resolution, error) {
	data, err := os.ReadFile(filepath.Join(root, ResolutionsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseResolutions(ResolutionsFile, data)
}

func ParseResolutions(file string, data []byte) ([]Resolution, error) {
	doc, err := validated(schemas.Resolutions, file, data)
	if err != nil {
		return nil, err
	}
	p := &problems{doc: doc}
	seen := map[[2]string]bool{}
	var out []Resolution
	for i, it := range doc.Value.([]any) {
		m := it.(map[string]any)
		r := Resolution{
			Path:       m["path"].(string),
			Function:   m["function"].(string),
			Resolution: m["resolution"].(string),
			Rationale:  m["rationale"].(string),
			Approver:   m["approver"].(string),
		}
		key := [2]string{r.Path, r.Function}
		if seen[key] {
			p.add(fmt.Sprintf("/%d", i), "unique", "duplicate resolution for %s %s", r.Path, r.Function)
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out, p.err(file)
}
