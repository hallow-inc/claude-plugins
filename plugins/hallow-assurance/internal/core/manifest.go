package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const ManifestName = "assurance.yaml"

type Component struct {
	Glob      Glob
	Level     Level
	Challenge string
	Formal    bool
	DST       bool
	Inputs    bool
}

type Manifest struct {
	Root         string
	DefaultLevel Level
	Languages    []string
	Components   []Component
	Protected    []Glob
	Catalog      Catalog
	Warnings     []schemas.Violation
}

var ErrNoManifest = errors.New("no " + ManifestName + " found in this directory or any parent")

func FindManifest(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, ManifestName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoManifest
		}
		dir = parent
	}
}

func LoadManifest(file string) (*Manifest, error) {
	file, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	m, err := parseManifest(file, data)
	if err != nil {
		return nil, err
	}
	m.Root = filepath.Dir(file)
	return m, nil
}

func parseManifest(file string, data []byte) (*Manifest, error) {
	doc, err := validated(schemas.Manifest, file, data)
	if err != nil {
		return nil, err
	}
	p := &problems{doc: doc}
	v := doc.Value.(map[string]any)
	m := &Manifest{DefaultLevel: Level(v["default_level"].(string))}
	for _, l := range v["languages"].([]any) {
		m.Languages = append(m.Languages, l.(string))
	}
	for i, c := range v["components"].([]any) {
		cm := c.(map[string]any)
		ptr := fmt.Sprintf("/components/%d", i)
		g, err := CompileGlob(cm["path"].(string))
		if err != nil {
			p.add(ptr+"/path", "glob", "%v", err)
		}
		comp := Component{Glob: g, Level: Level(cm["level"].(string))}
		if f, ok := cm["formal"].(map[string]any); ok {
			comp.Formal = true
			comp.Challenge = f["challenge"].(string)
		}
		_, comp.DST = cm["dst"]
		_, comp.Inputs = cm["inputs"]
		m.Components = append(m.Components, comp)
	}
	prot, _ := v["protected"].([]any)
	for i, s := range prot {
		g, err := CompileGlob(s.(string))
		if err != nil {
			p.add(fmt.Sprintf("/protected/%d", i), "glob", "%v", err)
		}
		m.Protected = append(m.Protected, g)
	}
	cat, err := LoadCatalog(v["catalog"].(string))
	if err != nil {
		p.add("/catalog", "catalog", "%v", err)
	}
	m.Catalog = cat
	if err := p.err(file); err != nil {
		return nil, err
	}
	m.Warnings = tieWarnings(m.Components, doc.Pos)
	return m, nil
}
