package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const WaiversFile = ".assure/waivers.yaml"

type Waiver struct {
	Objective string `json:"objective"`
	Scope     string `json:"scope"`
	Rationale string `json:"rationale"`
	Approver  string `json:"approver"`
	Expires   string `json:"expires"`
	glob      Glob
}

func (w Waiver) Active(date string) bool { return w.Expires >= date }

func LoadWaivers(root string) ([]Waiver, error) {
	data, err := os.ReadFile(filepath.Join(root, WaiversFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseWaivers(WaiversFile, data)
}

func ParseWaivers(file string, data []byte) ([]Waiver, error) {
	doc, err := validated(schemas.Waivers, file, data)
	if err != nil {
		return nil, err
	}
	p := &problems{doc: doc}
	var out []Waiver
	for i, it := range doc.Value.([]any) {
		m := it.(map[string]any)
		w := Waiver{
			Objective: m["objective"].(string),
			Scope:     m["scope"].(string),
			Rationale: m["rationale"].(string),
			Approver:  m["approver"].(string),
			Expires:   m["expires"].(string),
		}
		g, err := CompileGlob(w.Scope)
		if err != nil {
			p.add(fmt.Sprintf("/%d/scope", i), "glob", "%v", err)
			continue
		}
		w.glob = g
		out = append(out, w)
	}
	return out, p.err(file)
}

func (w Waiver) Match(path string) bool { return w.glob.Match(path) }

func (w Waiver) covers(paths []string) bool {
	for _, p := range paths {
		if !w.glob.Match(p) {
			return false
		}
	}
	return true
}

func activeFor(ws []Waiver, objective, date string) []Waiver {
	var out []Waiver
	for _, w := range ws {
		if w.Objective == objective && w.Active(date) {
			out = append(out, w)
		}
	}
	return out
}

func Expired(ws []Waiver, date string) []Waiver {
	var out []Waiver
	for _, w := range ws {
		if !w.Active(date) {
			out = append(out, w)
		}
	}
	return out
}
