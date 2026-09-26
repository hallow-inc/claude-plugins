package adapterproto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const CacheFile = ".assure/state/adapters.json"

type entry struct {
	Path     string          `json:"path"`
	SHA256   string          `json:"sha256"`
	Describe json.RawMessage `json:"describe"`
}

type cache struct {
	Version  int              `json:"version"`
	Adapters map[string]entry `json:"adapters"`
}

func readCache(root string) cache {
	c := cache{Adapters: map[string]entry{}}
	data, err := os.ReadFile(filepath.Join(root, CacheFile))
	if err != nil {
		return c
	}
	if vs, err := schemas.Validate(schemas.AdapterCache, data); err != nil || len(vs) > 0 {
		return c
	}
	var parsed cache
	if json.Unmarshal(data, &parsed) != nil {
		return c
	}
	return parsed
}

func writeCache(root string, c cache) error {
	dir := filepath.Join(root, filepath.Dir(CacheFile))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "adapters-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(root, CacheFile))
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Descriptions(root string, langs []string) (map[string]Describe, map[string]error) {
	c := readCache(root)
	out := map[string]Describe{}
	errs := map[string]error{}
	dirty := false
	for _, lang := range langs {
		exe, err := Resolve(lang)
		if err != nil {
			errs[lang] = err
			continue
		}
		sum, err := fileSHA256(exe)
		if err != nil {
			errs[lang] = &Error{Adapter: Executable(lang), Sub: "hash", Err: err}
			continue
		}
		if e, ok := c.Adapters[lang]; ok && e.Path == exe && e.SHA256 == sum {
			var d Describe
			if json.Unmarshal(e.Describe, &d) == nil {
				out[lang] = d
				continue
			}
		}
		d, raw, err := RunDescribe(exe, root, lang)
		if err != nil {
			delete(c.Adapters, lang)
			dirty = true
			errs[lang] = err
			continue
		}
		c.Adapters[lang] = entry{Path: exe, SHA256: sum, Describe: raw}
		dirty = true
		out[lang] = d
	}
	if dirty {
		c.Version = 0
		if err := writeCache(root, c); err != nil {
			errs[""] = fmt.Errorf("writing %s: %w", CacheFile, err)
		}
	}
	return out, errs
}
