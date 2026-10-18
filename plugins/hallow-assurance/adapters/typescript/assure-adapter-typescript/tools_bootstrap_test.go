package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type toolsReply struct {
	resp    toolsResponse
	entries []map[string]json.RawMessage
}

func toolsReplyOf(t fataler, dir string) toolsReply {
	_, raw := invoke(t, "tools", "--root", dir)
	if vs, err := schemas.Validate(schemas.AdapterTools, raw); err != nil || len(vs) > 0 {
		t.Fatalf("tools --root %s: response %s fails the protocol schema, so assure rejects it: %v %v", dir, raw, err, vs)
	}
	var r toolsReply
	var keys struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r.resp); err != nil {
		t.Fatalf("%v", err)
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("%v", err)
	}
	r.entries = keys.Tools
	protocolIs(t, "tools", r.resp.Protocol, raw)
	return r
}

// checkUntracked asserts every entry carries untracked: true when the
// lockfile is untracked, and no untracked key at all when it is tracked.
func checkUntracked(t fataler, r toolsReply, untracked bool) {
	for _, e := range r.entries {
		u, has := e["untracked"]
		if has != untracked || (has && string(u) != "true") {
			t.Fatalf("entry %s has untracked %s (present %v), want present %v; every tool is pinned by the one lockfile, so all carry untracked: true or none does, and the evaluator fails exactly the marked pins under authority: ci", e["name"], u, has, untracked)
		}
	}
}

// checkListing asserts the response lists all eight tools sorted by name,
// each pinned by lock with npm ci, carrying exactly the versions in installed
// and no version key at all for the rest, each marked untracked exactly when
// the lockfile is.
func checkListing(t fataler, r toolsReply, lock string, installed map[string]string, untracked bool) {
	if r.resp.Environment != nil {
		t.Fatalf("environment %q; a tool missing from node_modules is fixed by the install line, so the tools op must list it rather than fail, or assure tools --install-script cannot bootstrap a fresh checkout", r.resp.Environment.Message)
	}
	var want []toolEntry
	for _, n := range specToolNames {
		want = append(want, toolEntry{Name: n, Version: installed[n], PinnedBy: lock, Install: "npm ci"})
	}
	if !slices.Equal(r.resp.Tools, want) {
		t.Fatalf("got %+v\nwant %+v\nall eight tools sorted by name, with a version only where the package is installed", r.resp.Tools, want)
	}
	for i, e := range r.entries {
		_, has := e["version"]
		if name := want[i].Name; has != (installed[name] != "") {
			t.Fatalf("entry %d (%s) has version key = %v; the protocol marks a tool not installed by omitting version, and rejects an empty one", i, name, has)
		}
	}
	checkUntracked(t, r, untracked)
}

func npmRepo(t *testing.T) string {
	return gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, "package-lock.json")
}

func TestToolsPartlyInstalled(t *testing.T) {
	top := npmRepo(t)
	installTool(t, top, "vitest", "5.0.4")
	installBin(t, top, toolNamed(t, "vitest"))
	checkListing(t, toolsReplyOf(t, top), "package-lock.json", map[string]string{"vitest": "5.0.4"}, false)
}

func TestToolsInstalledOutOfRangeStillFails(t *testing.T) {
	top := npmRepo(t)
	installTool(t, top, "typescript", "7.0.2")
	installBin(t, top, toolNamed(t, "typescript"))
	r := toolsReplyOf(t, top)
	if r.resp.Environment == nil || len(r.resp.Tools) != 0 {
		t.Fatalf("got %+v; npm ci from the lockfile would reinstall typescript 7.0.2, so listing it as installable would let the script succeed and the next run fail", r.resp)
	}
	mustMention(t, r.resp.Environment.Message, top, "typescript", "7.0.2", "5.0.0", "6.1.0")
	if got := namedTools(r.resp.Environment.Message); !maps.Equal(got, map[string]bool{"typescript": true}) {
		t.Fatalf("message %q names %v; only typescript fails to resolve, the seven not installed are fixed by npm ci", r.resp.Environment.Message, got)
	}
}

func TestToolsUntrackedLockfileWithoutDependencies(t *testing.T) {
	top := gitRepo(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"})
	checkListing(t, toolsReplyOf(t, top), "package-lock.json", nil, true)
}

type installState int

const (
	stateAbsent installState = iota
	stateInRange
	stateOutOfRange
	stateInexact
	stateNoBin
)

// TestToolsInstallStates draws, per tool, whether it is absent, installed in
// range, or installed in a way npm ci would not fix, and whether the lockfile
// is tracked, and checks the tools op lists the first two, marked untracked
// exactly when the lockfile is, and reports environment naming exactly the
// third.
func TestToolsInstallStates(t *testing.T) {
	top := gitRepo(t, nil)
	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(top, "r")
		if err != nil {
			t.Fatalf("%v", err)
		}
		writeTree(t, dir, map[string]string{"package.json": "{}", "package-lock.json": "{}"})
		tracked := rapid.Bool().Draw(t, "tracked")
		if tracked {
			git(t, top, "add", "--", filepath.Base(dir)+"/package-lock.json")
		}

		installed := map[string]string{}
		failing := map[string]bool{}
		for _, tool := range tsTools {
			states := []installState{stateAbsent, stateInRange, stateOutOfRange, stateInexact}
			if tool.bin != "" {
				states = append(states, stateNoBin)
			}
			switch rapid.SampledFrom(states).Draw(t, tool.name) {
			case stateAbsent:
			case stateInRange:
				v := rapid.SampledFrom([]string{tool.lo, justBelow(tool.hi)}).Draw(t, tool.name+" version")
				installTool(t, dir, tool.name, v)
				installBin(t, dir, tool)
				installed[tool.name] = v
			case stateOutOfRange:
				installTool(t, dir, tool.name, rapid.SampledFrom([]string{justBelow(tool.lo), tool.hi}).Draw(t, tool.name+" version"))
				installBin(t, dir, tool)
				failing[tool.name] = true
			case stateInexact:
				installTool(t, dir, tool.name, tool.lo+"-rc.1")
				installBin(t, dir, tool)
				failing[tool.name] = true
			case stateNoBin:
				installTool(t, dir, tool.name, tool.lo)
				failing[tool.name] = true
			}
		}

		r := toolsReplyOf(t, dir)
		if len(failing) == 0 {
			checkListing(t, r, filepath.Base(dir)+"/package-lock.json", installed, !tracked)
			return
		}
		if r.resp.Environment == nil || len(r.resp.Tools) != 0 {
			t.Fatalf("got %+v; %v are installed but unusable and npm ci would not fix them, so the op must report environment and list nothing", r.resp, slices.Sorted(maps.Keys(failing)))
		}
		if got := namedTools(r.resp.Environment.Message); !maps.Equal(got, failing) {
			t.Fatalf("message %q names %v, want exactly %v; a tool merely not installed is not a failure for the tools op", r.resp.Environment.Message, got, failing)
		}
	})
}
