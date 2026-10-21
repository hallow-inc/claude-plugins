package main

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
)

func rawReport(t *testing.T, r repo) map[string]json.RawMessage {
	t.Helper()
	readReport(t, r)
	data, err := os.ReadFile(filepath.Join(r.root, app.ReportFile))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func reportAuthority(t *testing.T, r repo) (string, map[string][]string) {
	t.Helper()
	raw := rawReport(t, r)
	var authority string
	if err := json.Unmarshal(raw["authority"], &authority); err != nil {
		t.Fatalf("report authority %s: %v", raw["authority"], err)
	}
	var pins map[string][]string
	if err := json.Unmarshal(raw["untracked_pins"], &pins); err != nil || pins == nil {
		t.Fatalf("report untracked_pins %s must be an object: %v", raw["untracked_pins"], err)
	}
	return authority, pins
}

func TestEvaluateAuthorityAcrossManifestTracking(t *testing.T) {
	for _, key := range []string{"", "local", "ci"} {
		for _, excluded := range []bool{false, true} {
			name := cmp.Or(key, "absent")
			if excluded {
				name += ", excluded"
			}
			t.Run(name, func(t *testing.T) { checkAuthority(t, key, excluded) })
		}
	}
}

func checkAuthority(t *testing.T, key string, excluded bool) {
	manifest := xxManifest
	if key != "" {
		manifest += "authority: " + key + "\n"
	}
	f := provBase(t, manifest, func(f provFixture) {
		if excluded {
			f.write(".git/info/exclude", "assurance.yaml\n")
		}
	})
	handEdit(f)
	f.commit()
	code, stdout, stderr := assure("evaluate", "--changed-from", "main", "--date", "2026-10-01")
	if key == "ci" && excluded {
		if code != 2 {
			t.Fatalf("exit %d; CI cannot read an excluded manifest, so evaluate must refuse\n%s%s", code, stdout, stderr)
		}
		noReport(t, f.repo)
		return
	}
	if code == 2 {
		t.Fatalf("exit 2:\n%s%s", stdout, stderr)
	}
	want := cmp.Or(key, "local")
	authority, pins := reportAuthority(t, f.repo)
	if authority != want {
		t.Errorf("report authority %q, want %q: an absent key must never read as CI-gated", authority, want)
	}
	if len(pins) != 0 {
		t.Errorf("untracked_pins %v with no adapter run", pins)
	}
	first, _, _ := strings.Cut(stdout, "\n")
	if strings.Contains(first, "self-attested") != (want == "local") || (want == "ci" && strings.Contains(stdout, "self-attested")) {
		t.Errorf("summary under %s:\n%s\nself-attested must label the first line of exactly the local verdicts", want, stdout)
	}
	if rep, _ := readReport(t, f.repo); !slices.ContainsFunc(rep.Objectives, func(e app.Entry) bool { return e.Objective == "IND-VERIFIER-DISTINCT" }) {
		t.Errorf("IND-VERIFIER-DISTINCT absent: the manifest turns provenance on and, tracked at main or excluded and so standing in for the base, must make it applicable")
	}
}

func untrackedPinRepo(t *testing.T, manifest string) repo {
	t.Helper()
	xx := passingXXResponses()
	xx["VER-MUTATION-CHANGED.json"] = mutationResponse("mise.local.toml", true)
	return cannedXXRepo(t, manifest, xx)
}

func TestEvaluateUntrackedPinFailsOnlyUnderCentralAuthority(t *testing.T) {
	t.Run("ci", func(t *testing.T) {
		r := untrackedPinRepo(t, xxManifest+"authority: ci\n")
		code, stdout, stderr := assure("evaluate", "--changed-from", "main", "--date", "2026-10-01")
		if code != 1 {
			t.Fatalf("exit %d, want 1: a pin CI cannot see blocks a CI verdict\n%s%s", code, stdout, stderr)
		}
		rep, _ := readReport(t, r)
		if e, ok := entry(rep, "VER-MUTATION-CHANGED", "xx"); !ok || e.Status != "fail" || !detailWith(e, "gremlins", "mise.local.toml") {
			t.Fatalf("VER-MUTATION-CHANGED %+v; want fail with a detail naming the tool and its pin file", e)
		}
		if e, _ := entry(rep, "VER-TESTS-PASS", "xx"); e.Status != "pass" {
			t.Fatalf("VER-TESTS-PASS %+v; only the objective whose run marked the pin may fail", e)
		}
	})
	t.Run("local", func(t *testing.T) {
		r := untrackedPinRepo(t, xxManifest)
		code, stdout, stderr := assure("evaluate", "--changed-from", "main", "--date", "2026-10-01")
		if code == 2 {
			t.Fatalf("exit 2:\n%s%s", stdout, stderr)
		}
		rep, _ := readReport(t, r)
		if e, ok := entry(rep, "VER-MUTATION-CHANGED", "xx"); !ok || e.Status != "pass" {
			t.Fatalf("VER-MUTATION-CHANGED %+v; every mutant was killed, and an untracked pin must not change a local verdict", e)
		}
		if rep.PinnedBy["xx"]["gremlins"] != "mise.local.toml" {
			t.Fatalf("pinned_by %v", rep.PinnedBy)
		}
		if _, pins := reportAuthority(t, r); !slices.Equal(pins["xx"], []string{"gremlins"}) || len(pins) != 1 {
			t.Fatalf("untracked_pins %v, want {xx: [gremlins]}", pins)
		}
		if !hasLine(stdout, "gremlins", "untracked") {
			t.Fatalf("summary does not list gremlins as untracked:\n%s", stdout)
		}
	})
}
