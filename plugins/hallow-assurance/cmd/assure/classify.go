package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
)

type claim struct {
	lang string
	role string
}

func runClassify(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure classify <path>...")
		return 2
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 1
	}
	rels, err := relPaths(m, args)
	if err != nil {
		return fail(err)
	}
	requested := map[string]bool{}
	for _, r := range rels {
		requested[r] = true
	}
	claims := map[string]claim{}
	for _, lang := range m.Languages {
		files, err := adapterproto.Classify(m.Root, lang, rels)
		if err != nil {
			return fail(err)
		}
		for _, f := range files {
			if !requested[f.Path] {
				return fail(fmt.Errorf("%s classify returned %s, which was not requested", adapterproto.Executable(lang), f.Path))
			}
			if prev, ok := claims[f.Path]; ok && prev.role != f.Role {
				return fail(fmt.Errorf("%s is ambiguous: %s says %s, %s says %s", f.Path, adapterproto.Executable(prev.lang), prev.role, adapterproto.Executable(lang), f.Role))
			} else if !ok {
				claims[f.Path] = claim{lang: lang, role: f.Role}
			}
		}
	}
	var b strings.Builder
	for i, rel := range rels {
		_, level := m.Resolve(rel)
		c, ok := claims[rel]
		if !ok {
			c = claim{lang: "-", role: "unclassified"}
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", args[i], level, c.lang, c.role)
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(err)
	}
	return 0
}
