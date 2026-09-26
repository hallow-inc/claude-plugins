package catalog

import (
	"embed"
	"io/fs"
)

//go:embed v*/objectives.yaml
var files embed.FS

func Version(v string) ([]byte, bool) {
	data, err := fs.ReadFile(files, v+"/objectives.yaml")
	return data, err == nil
}
