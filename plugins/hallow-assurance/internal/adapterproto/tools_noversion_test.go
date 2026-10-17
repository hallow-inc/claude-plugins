package adapterproto

import (
	"encoding/json"
	"os"
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

func TestRunToolsReadsAbsentVersionAsNotInstalled(t *testing.T) {
	resp := respondingAdapter(t, "")
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 4).Draw(rt, "n")
		names := []string{"vitest", "eslint", "typescript", "gremlins"}[:n]
		var entries []map[string]string
		var want []Tool
		emptyAt := -1
		for i, name := range names {
			tool := toolGen.Draw(rt, "tool")
			tool.Name = name
			e := map[string]string{"name": tool.Name, "pinned_by": tool.PinnedBy, "install": tool.Install}
			switch rapid.SampledFrom([]versionForm{versionPresent, versionAbsent, versionEmpty}).Draw(rt, "version form") {
			case versionPresent:
				e["version"] = tool.Version
			case versionAbsent:
				tool.Version = ""
			case versionEmpty:
				e["version"] = ""
				tool.Version = ""
				if emptyAt < 0 {
					emptyAt = i
				}
			}
			entries = append(entries, e)
			want = append(want, tool)
		}
		data, err := json.Marshal(map[string]any{"protocol": 1, "tools": entries})
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.WriteFile(resp, data, 0o644); err != nil {
			rt.Fatal(err)
		}
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
