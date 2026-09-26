package core

import (
	"fmt"
	"path"
	"strings"
)

type Glob struct {
	src  string
	segs []string
}

func CompileGlob(src string) (Glob, error) {
	segs := strings.Split(src, "/")
	for _, s := range segs {
		if s == "**" {
			continue
		}
		if _, err := path.Match(s, ""); err != nil {
			return Glob{}, fmt.Errorf("malformed glob %q: %w", src, err)
		}
	}
	return Glob{src: src, segs: segs}, nil
}

func (g Glob) String() string { return g.src }

func (g Glob) Match(p string) bool {
	return matchSegs(g.segs, strings.Split(p, "/"))
}

func matchSegs(segs, parts []string) bool {
	for len(segs) > 0 {
		if segs[0] == "**" {
			for i := 0; i <= len(parts); i++ {
				if matchSegs(segs[1:], parts[i:]) {
					return true
				}
			}
			return false
		}
		if len(parts) == 0 {
			return false
		}
		if ok, _ := path.Match(segs[0], parts[0]); !ok {
			return false
		}
		segs, parts = segs[1:], parts[1:]
	}
	return len(parts) == 0
}

func literal(seg string) bool {
	return !strings.ContainsAny(seg, `*?[\`)
}

func (g Glob) Literals() int {
	n := 0
	for _, s := range g.segs {
		if literal(s) {
			n++
		}
	}
	return n
}
