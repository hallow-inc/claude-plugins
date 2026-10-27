package adapterkit

import (
	"os"
	"path/filepath"
)

func Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func RelSlash(top, p string) string {
	rel, err := filepath.Rel(top, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func FindUp(top, unit, rel string) (string, bool) {
	for dir := unit; ; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, rel)
		if Exists(p) {
			return p, true
		}
		if dir == top || filepath.Dir(dir) == dir {
			return "", false
		}
	}
}
