package main

import (
	"cmp"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

type pinnedTool struct {
	name    string
	keys    []string
	def     string
	lo, hi  string
	install func(version string) string
}

var pinnedTools = []pinnedTool{
	{
		name: "golangci-lint",
		keys: []string{"golangci-lint", "aqua:golangci/golangci-lint"},
		def:  "2.13.2",
		lo:   "2.13.2", hi: "3.0.0",
		install: func(v string) string {
			return `curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v` + v
		},
	},
	{
		name: "gremlins",
		keys: []string{"go:github.com/go-gremlins/gremlins/cmd/gremlins"},
		def:  "0.6.0",
		lo:   "0.6.0", hi: "0.7.0",
		install: func(v string) string {
			return "go install github.com/go-gremlins/gremlins/cmd/gremlins@v" + v
		},
	},
}

var objectiveTools = map[string][]string{
	"CODE-ZERO-WARNINGS":   {"golangci-lint"},
	"CODE-CHECK-RETURNS":   {"golangci-lint"},
	"CODE-RESOURCE-BOUNDS": {"golangci-lint"},
	"VER-MUTATION-CHANGED": {"gremlins"},
}

var miseConfigNames = []string{"mise.toml", ".mise.toml", ".config/mise.toml", "mise/config.toml", ".tool-versions"}

var exactPin = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

func stripV(v string) string {
	return strings.TrimPrefix(v, "v")
}

func isExact(pin string) bool {
	return exactPin.MatchString(pin)
}

func compareVersions(a, b string) int {
	as, bs := strings.Split(stripV(a), "."), strings.Split(stripV(b), ".")
	for i := range 3 {
		x, y := strings.TrimLeft(as[i], "0"), strings.TrimLeft(bs[i], "0")
		if c := cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y)); c != 0 {
			return c
		}
	}
	return 0
}

func sameVersion(pinned, found string) bool {
	return isExact(pinned) && isExact(found) && compareVersions(pinned, found) == 0
}

func (t pinnedTool) inRange(v string) bool {
	return isExact(v) && compareVersions(v, t.lo) >= 0 && compareVersions(v, t.hi) < 0
}

func (t pinnedTool) rangeText() string {
	return fmt.Sprintf(">= %s, < %s", t.lo, t.hi)
}

type miseSource struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type miseEntry struct {
	Version          string     `json:"version"`
	RequestedVersion string     `json:"requested_version"`
	Source           miseSource `json:"source"`
}

type probe struct {
	top            string
	miseAbsent     bool
	miseErr        string
	mise           map[string][]miseEntry
	trackedConfigs []string
	tracked        map[string]bool
}

type resolved struct {
	name, version, pinnedBy, key, install string
}

type resolution struct {
	tool    pinnedTool
	res     resolved
	problem string
}

type candidate struct {
	key, pin, file string
}

func relToTop(top, p string) (string, bool) {
	top, p = path.Clean(top), path.Clean(p)
	if !strings.HasPrefix(p, top+"/") {
		return "", false
	}
	return strings.TrimPrefix(p, top+"/"), true
}

func pinLine(file, key, version string) string {
	if path.Base(file) == ".tool-versions" {
		return key + " " + version
	}
	return fmt.Sprintf("%q = %q", key, version)
}

func resolveTool(p probe, t pinnedTool) resolution {
	fail := func(format string, args ...any) resolution {
		return resolution{tool: t, problem: t.name + ": " + fmt.Sprintf(format, args...)}
	}
	if p.miseAbsent && len(p.trackedConfigs) > 0 {
		return fail("%s is a tracked mise config, but mise is not on PATH; install mise (https://mise.jdx.dev) so the adapter reads the same pins CI does", p.trackedConfigs[0])
	}
	if p.miseErr != "" {
		return fail("%s", p.miseErr)
	}
	var found []candidate
	for _, key := range t.keys {
		for _, e := range p.mise[key] {
			if e.Source.Path == "" {
				continue
			}
			rel, inside := relToTop(p.top, e.Source.Path)
			if !inside {
				continue
			}
			if !p.tracked[rel] {
				return fail("%s pins %s but is not tracked by git, so CI would not see it; commit the pin or remove it", rel, key)
			}
			c := candidate{key: key, pin: e.RequestedVersion, file: rel}
			if !slices.Contains(found, c) {
				found = append(found, c)
			}
		}
	}
	switch len(found) {
	case 0:
		return resolution{tool: t, res: resolved{name: t.name, version: t.def, pinnedBy: "default", install: t.install(t.def)}}
	case 1:
	default:
		var where []string
		for _, c := range found {
			where = append(where, fmt.Sprintf("%s in %s", pinLine(c.file, c.key, c.pin), c.file))
		}
		return fail("pinned more than once (%s); keep one pin", strings.Join(where, "; "))
	}
	c := found[0]
	if !isExact(c.pin) {
		return fail("%s pins %q, which is not an exact version; pin an exact version instead, for example %s", c.file, c.pin, pinLine(c.file, c.key, t.def))
	}
	v := stripV(c.pin)
	if !t.inRange(v) {
		return fail("%s pins %s, outside the supported range %s", c.file, v, t.rangeText())
	}
	return resolution{tool: t, res: resolved{name: t.name, version: v, pinnedBy: c.file, key: c.key, install: "mise install " + c.key + "@" + v}}
}

func resolve(p probe) []resolution {
	out := make([]resolution, len(pinnedTools))
	for i, t := range pinnedTools {
		out[i] = resolveTool(p, t)
	}
	return out
}
