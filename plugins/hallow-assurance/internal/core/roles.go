package core

import (
	"fmt"
	"sort"
)

type Role string

const (
	Source       Role = "source"
	Test         Role = "test"
	Generated    Role = "generated"
	FuzzCorpus   Role = "fuzz_corpus"
	Config       Role = "config"
	Unclassified Role = "unclassified"
)

var precedence = []Role{Generated, FuzzCorpus, Test, Config}

type RolePatterns struct {
	Claims   []string
	Patterns map[string][]string
}

type adapterGlobs struct {
	lang     string
	claims   []Glob
	patterns map[Role][]Glob
}

type Roles struct {
	adapters []adapterGlobs
}

func compileAll(srcs []string) ([]Glob, error) {
	out := make([]Glob, 0, len(srcs))
	for _, s := range srcs {
		g, err := CompileGlob(s)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func NewRoles(byLang map[string]RolePatterns) (Roles, error) {
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	var r Roles
	for _, l := range langs {
		rp := byLang[l]
		a := adapterGlobs{lang: l, patterns: map[Role][]Glob{}}
		var err error
		if a.claims, err = compileAll(rp.Claims); err != nil {
			return Roles{}, fmt.Errorf("assure-adapter-%s describe: %w", l, err)
		}
		for _, role := range precedence {
			if a.patterns[role], err = compileAll(rp.Patterns[string(role)]); err != nil {
				return Roles{}, fmt.Errorf("assure-adapter-%s describe: %w", l, err)
			}
		}
		r.adapters = append(r.adapters, a)
	}
	return r, nil
}

func anyMatch(gs []Glob, p string) bool {
	for _, g := range gs {
		if g.Match(p) {
			return true
		}
	}
	return false
}

func (a adapterGlobs) role(p string) (Role, bool) {
	for _, role := range precedence {
		if anyMatch(a.patterns[role], p) {
			return role, true
		}
	}
	if anyMatch(a.claims, p) {
		return Source, true
	}
	return "", false
}

func (r Roles) Of(p string) (Role, error) {
	role, owner := Unclassified, ""
	for _, a := range r.adapters {
		got, ok := a.role(p)
		if !ok {
			continue
		}
		if owner != "" && got != role {
			return "", fmt.Errorf("%s is ambiguous: assure-adapter-%s says %s, assure-adapter-%s says %s", p, owner, role, a.lang, got)
		}
		role, owner = got, a.lang
	}
	return role, nil
}
