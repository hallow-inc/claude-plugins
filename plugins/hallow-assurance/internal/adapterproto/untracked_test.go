package adapterproto

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

var pinTools = []string{"gremlins", "vitest", "@vitest/coverage-v8", "@stryker-mutator/core", "fast-check"}

func TestRunRejectsUntrackedToolWithoutPin(t *testing.T) {
	resp := respondingAdapter(t, "")
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		pinned := rapid.SliceOfDistinct(rapid.SampledFrom(pinTools), rapid.ID[string]).Draw(rt, "pinned")
		untracked := rapid.SliceOfNDistinct(rapid.SampledFrom(pinTools), 0, 3, rapid.ID[string]).Draw(rt, "untracked")
		versions := map[string]string{"go": "go1.26.7"}
		pinnedBy := map[string]string{}
		for _, tool := range pinned {
			versions[tool] = "1.2.3"
			pinnedBy[tool] = rapid.SampledFrom([]string{"default", "mise.toml", "mise.local.toml", "package-lock.json"}).Draw(rt, "pin "+tool)
		}
		body := map[string]any{"protocol": 2, "evidence": []any{}, "tool_versions": versions, "pinned_by": pinnedBy}
		if len(untracked) > 0 {
			body["untracked"] = untracked
		}
		data, err := json.Marshal(body)
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.WriteFile(resp, data, 0o644); err != nil {
			rt.Fatal(err)
		}
		var orphans []string
		for _, tool := range untracked {
			if _, ok := pinnedBy[tool]; !ok {
				orphans = append(orphans, tool)
			}
		}
		got, err := RunObjective(dir, "go", "VER-MUTATION-CHANGED", "main", "ev", 5*time.Second)
		if len(orphans) > 0 {
			ae, ok := errors.AsType[*Error](err)
			if !ok || ae.Adapter != "assure-adapter-go" || ae.Sub != "run" ||
				!slices.ContainsFunc(orphans, func(tool string) bool { return strings.Contains(err.Error(), tool) }) {
				rt.Fatalf("response %s: got %+v, %v; an untracked tool with no pin cannot be reported with its pinned_by, so the client must reject it naming assure-adapter-go run and one of %v", data, got, err, orphans)
			}
			return
		}
		if err != nil {
			rt.Fatalf("response %s rejected: %v; every untracked name is a pinned_by key", data, err)
		}
		if !slices.Equal(got.Untracked, untracked) {
			rt.Fatalf("response %s: Untracked %v, want %v; the evaluator fails untracked pins under authority: ci only if the client carries them", data, got.Untracked, untracked)
		}
	})
}
