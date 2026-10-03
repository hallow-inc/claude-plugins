package app

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func LoadRoles(m *core.Manifest) (roles core.Roles, failures []error, cacheErr error) {
	ds, errs := adapterproto.Descriptions(m.Root, m.Languages)
	for _, lang := range m.Languages {
		if err := errs[lang]; err != nil {
			failures = append(failures, err)
		}
	}
	patterns := map[string]core.RolePatterns{}
	for lang, d := range ds {
		patterns[lang] = core.RolePatterns{Claims: d.Claims, Patterns: d.Patterns}
	}
	roles, err := core.NewRoles(patterns)
	if err != nil {
		failures = append(failures, err)
	}
	return roles, failures, errs[""]
}

func JoinErrors(errs []error) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	sort.Strings(msgs)
	return strings.Join(msgs, "; ")
}

func ManifestFor(dir string) (*core.Manifest, error) {
	file, err := core.FindManifest(dir)
	if err != nil {
		return nil, err
	}
	m, err := core.LoadManifest(file)
	if err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	return m, nil
}

func References(m *core.Manifest) (map[string]string, []error) {
	ds, errs := adapterproto.Descriptions(m.Root, m.Languages)
	out := map[string]string{}
	for _, lang := range m.Languages {
		if d, ok := ds[lang]; ok && d.Reference != "" {
			out[d.Reference] = lang
		}
	}
	var failures []error
	for _, lang := range append(slices.Clone(m.Languages), "") {
		if err := errs[lang]; err != nil {
			failures = append(failures, err)
		}
	}
	return out, failures
}
