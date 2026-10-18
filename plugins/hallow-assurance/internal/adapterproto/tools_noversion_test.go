package adapterproto

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type versionForm int

const (
	versionPresent versionForm = iota
	versionAbsent
	versionEmpty
)

func drawVersionedEntry(rt *rapid.T, name string) (entry map[string]any, want Tool, emptyVersion bool) {
	want = toolGen.Draw(rt, "tool")
	want.Name = name
	entry = toolEntry(want)
	switch rapid.SampledFrom([]versionForm{versionPresent, versionAbsent, versionEmpty}).Draw(rt, "version form") {
	case versionPresent:
		entry["version"] = want.Version
	case versionAbsent:
		want.Version = ""
	case versionEmpty:
		entry["version"] = ""
		want.Version = ""
		emptyVersion = true
	}
	return entry, want, emptyVersion
}

func TestRunToolsReadsAbsentVersionAsNotInstalled(t *testing.T) {
	resp := respondingAdapter(t, "")
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 4).Draw(rt, "n")
		names := []string{"vitest", "eslint", "typescript", "gremlins"}[:n]
		var entries []map[string]any
		var want []Tool
		emptyAt := -1
		for i, name := range names {
			e, tool, empty := drawVersionedEntry(rt, name)
			if empty && emptyAt < 0 {
				emptyAt = i
			}
			entries = append(entries, e)
			want = append(want, tool)
		}
		data := writeResponse(rt, resp, map[string]any{"protocol": 2, "tools": entries})
		got, err := RunTools(dir, "go")
		if emptyAt >= 0 {
			if err == nil || !strings.Contains(err.Error(), "violates the protocol") || !strings.Contains(err.Error(), "version") {
				rt.Fatalf("response %s: got %+v, %v; an empty version is neither installed nor absent, so the client must reject it naming version", data, got, err)
			}
			return
		}
		if err != nil {
			rt.Fatalf("response %s rejected: %v; an entry without version means not installed yet, and rejecting it stops assure tools printing the install line that would install it", data, err)
		}
		if got.Environment != nil || !slices.Equal(got.Tools, want) {
			rt.Fatalf("got %+v\nwant %+v\nan absent version must read as an empty Version with every other field unchanged", got.Tools, want)
		}
	})
}
