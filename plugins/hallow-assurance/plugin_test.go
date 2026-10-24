package hallowassurance_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func shim(t *testing.T, dir, path, stdin string) (int, string, string) {
	t.Helper()
	return shimEvent(t, "stop", dir, path, stdin)
}

func shimEvent(t *testing.T, event, dir, path, stdin string) (int, string, string) {
	t.Helper()
	abs, err := filepath.Abs("plugin/bin/assure-hook")
	if err != nil {
		t.Fatal(err)
	}
	return shimAt(t, abs, event, dir, path, stdin)
}

func shimAt(t *testing.T, abs, event, dir, path, stdin string) (int, string, string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "/bin/sh", abs, event)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + path, "PWD=" + dir}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func fakeAssure(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "assure"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir + ":/usr/bin:/bin"
}

func adoptedDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assurance.yaml"), []byte("version: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestShimIsInertWithoutManifest(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if code, out, errb := shim(t, dir, "/usr/bin:/bin", "{}"); code != 0 || out != "" || errb != "" {
		t.Fatalf("got %d %q %q", code, out, errb)
	}
}

func TestShimExecsAssureWithStdin(t *testing.T) {
	path := fakeAssure(t, `if [ "$1 $2" = "hook protocol" ]; then echo 3; exit 0; fi
echo "args=$*"; cat; exit 2`)
	for _, event := range []string{"session-start", "stop"} {
		code, out, _ := shimEvent(t, event, adoptedDir(t), path, `{"hook_event_name":"X"}`)
		if code != 2 || out != "args=hook "+event+"\n{\"hook_event_name\":\"X\"}" {
			t.Fatalf("%s: got %d %q", event, code, out)
		}
	}
}

func TestHooksJSONDeclaresEveryHookWithATimeout(t *testing.T) {
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Timeout int      `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(read(t, "plugin/hooks/hooks.json"), &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		matcher, arg string
		timeout      int
	}{
		"SessionStart": {"", "session-start", 30},
		"Stop":         {"", "stop", 180},
	}
	if len(cfg.Hooks) != len(want) {
		t.Fatalf("events %v, want exactly %v", cfg.Hooks, want)
	}
	for event, w := range want {
		groups := cfg.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: want one hook, got %+v", event, groups)
		}
		h := groups[0].Hooks[0]
		if groups[0].Matcher != w.matcher || h.Type != "command" || h.Command != "${CLAUDE_PLUGIN_ROOT}/bin/assure-hook" ||
			len(h.Args) != 1 || h.Args[0] != w.arg || h.Timeout != w.timeout {
			t.Errorf("%s: got matcher %q %+v; a hook without an explicit timeout waits 600 s and then fails open", event, groups[0].Matcher, h)
		}
	}
}

func TestMarketplaceListsThePlugin(t *testing.T) {
	var mk struct {
		Plugins []struct{ Name, Source string } `json:"plugins"`
	}
	if err := json.Unmarshal(read(t, "../../.claude-plugin/marketplace.json"), &mk); err != nil {
		t.Fatal(err)
	}
	for _, p := range mk.Plugins {
		if p.Name == "hallow-assurance" && p.Source == "./plugins/hallow-assurance/plugin" {
			return
		}
	}
	t.Fatalf("marketplace has no hallow-assurance entry pointing at ./plugins/hallow-assurance/plugin: %+v", mk.Plugins)
}

func stopMessage(t *testing.T, code int, out string) string {
	t.Helper()
	var o struct {
		SystemMessage string `json:"systemMessage"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &o) != nil || o.SystemMessage == "" {
		t.Fatalf("the shim must exit 0 with a JSON systemMessage: got %d %q", code, out)
	}
	return o.SystemMessage
}

func TestShimReportsMissingAssureWithoutBlocking(t *testing.T) {
	for _, event := range []string{"session-start", "stop"} {
		code, out, _ := shimEvent(t, event, adoptedDir(t), "/usr/bin:/bin", "{}")
		msg := stopMessage(t, code, out)
		if !strings.Contains(msg, "not on PATH") {
			t.Fatalf("%s: message %q does not say assure is missing", event, msg)
		}
		checkRealInstallAdvice(t, msg)
	}
}

const installPrefix = "go install github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/...@v"

func pluginJSON(version string) string {
	v := ""
	if version != "" {
		v = "  \"version\": \"" + version + "\",\n"
	}
	return "{\n  \"$schema\": \"https://json.schemastore.org/claude-code-plugin-manifest.json\",\n  \"name\": \"hallow-assurance\",\n" + v +
		"  \"description\": \"Verification in proportion to criticality.\",\n  \"license\": \"LicenseRef-Hallow-Internal\",\n  \"keywords\": [\"verification\", \"assurance\"]\n}\n"
}

func pluginCopy(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shimPath := filepath.Join(root, "bin", "assure-hook")
	if err := os.WriteFile(shimPath, read(t, "plugin/bin/assure-hook"), 0o755); err != nil {
		t.Fatal(err)
	}
	return shimPath
}

func strictStopMessage(t *testing.T, code int, out string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var o map[string]any
	if err := dec.Decode(&o); err != nil || code != 0 {
		t.Fatalf("the shim must exit 0 with one JSON object: got %d %q (%v)", code, out, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON value: %q", out)
	}
	msg, ok := o["systemMessage"].(string)
	if !ok || msg == "" {
		t.Fatalf("no systemMessage in %q", out)
	}
	return msg
}

type unavailableCase struct {
	name string
	path func(t *testing.T) string
}

func unavailableCases() []unavailableCase {
	return []unavailableCase{
		{"assure missing", func(*testing.T) string { return "/usr/bin:/bin" }},
		{"protocol mismatch", func(t *testing.T) string { return fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && echo 7`) }},
	}
}

func shimMessages(t *testing.T, shimPath string, c unavailableCase) map[string]string {
	t.Helper()
	msgs := map[string]string{}
	for _, event := range []string{"session-start", "stop"} {
		code, out, _ := shimAt(t, shimPath, event, adoptedDir(t), c.path(t), "{}")
		msgs[event] = strictStopMessage(t, code, out)
	}
	return msgs
}

func checkNamesNoAdapter(t *testing.T, where, msg string) {
	t.Helper()
	if strings.Contains(msg, "adapter") || strings.Contains(msg, "<lang>") || languageNames.MatchString(msg) {
		t.Errorf("%s: install advice %q names an adapter or language; one install line covers every adapter (design invariant 1)", where, msg)
	}
}

func TestShimInstallLineComesFromPluginVersion(t *testing.T) {
	for _, v := range []string{"0.2.0", "1.10.3", "12.0.105"} {
		shimPath := pluginCopy(t, pluginJSON(v))
		for _, c := range unavailableCases() {
			for event, msg := range shimMessages(t, shimPath, c) {
				if !strings.Contains(msg, installPrefix+v) {
					t.Errorf("version %s, %s at %s: message %q lacks %q; the advice must install the release this plugin ships with", v, c.name, event, msg, installPrefix+v)
				}
				checkNamesNoAdapter(t, c.name+" at "+event, msg)
			}
		}
	}
}

func TestShimWithoutPluginVersionPointsToReadme(t *testing.T) {
	for name, manifest := range map[string]string{"no version key": pluginJSON(""), "no plugin.json": ""} {
		shimPath := pluginCopy(t, manifest)
		for _, c := range unavailableCases() {
			for event, msg := range shimMessages(t, shimPath, c) {
				if !strings.Contains(msg, "README") || strings.Contains(msg, "go install") {
					t.Errorf("%s, %s at %s: message %q; with no readable version the advice must point to the README and name no version", name, c.name, event, msg)
				}
				checkNamesNoAdapter(t, name+", "+c.name+" at "+event, msg)
			}
		}
	}
}

func checkRealInstallAdvice(t *testing.T, msg string) {
	t.Helper()
	var p struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(read(t, "plugin/.claude-plugin/plugin.json"), &p); err != nil {
		t.Fatal(err)
	}
	if p.Version != "" {
		if !strings.Contains(msg, installPrefix+p.Version) {
			t.Errorf("real plugin.json has version %s; message %q lacks %q", p.Version, msg, installPrefix+p.Version)
		}
	} else if !strings.Contains(msg, "README") {
		t.Errorf("real plugin.json has no version; message %q does not point to the README", msg)
	}
	checkNamesNoAdapter(t, "real plugin", msg)
}

func TestShimAndHooksNameNoAdapterOrLanguage(t *testing.T) {
	for _, p := range []string{"plugin/bin/assure-hook", "plugin/hooks/hooks.json"} {
		for i, line := range strings.Split(string(read(t, p)), "\n") {
			if strings.Contains(line, "assure-adapter") || strings.Contains(line, "<lang>") || languageNames.MatchString(line) {
				t.Errorf("%s:%d names an adapter or language: %s", p, i+1, strings.TrimSpace(line))
			}
		}
	}
}

var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func TestPluginJSONCarriesSemverVersion(t *testing.T) {
	var p map[string]any
	if err := json.Unmarshal(read(t, "plugin/.claude-plugin/plugin.json"), &p); err != nil {
		t.Fatal(err)
	}
	v, ok := p["version"].(string)
	if !ok || !semverRe.MatchString(v) {
		t.Fatalf("plugin.json version %#v; the shim's install line and the release check both read X.Y.Z from it", p["version"])
	}
}

func TestMarketplaceEntryCarriesNoVersion(t *testing.T) {
	var mk struct {
		Plugins []map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(read(t, "../../.claude-plugin/marketplace.json"), &mk); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range mk.Plugins {
		if p["name"] != "hallow-assurance" {
			continue
		}
		found = true
		if v, ok := p["version"]; ok {
			t.Errorf("marketplace hallow-assurance entry has version %#v; plugin.json silently overrides it, so two versions would disagree", v)
		}
	}
	if !found {
		t.Fatal("marketplace has no hallow-assurance entry")
	}
}

func TestShimReportsProtocolMismatchWithoutBlocking(t *testing.T) {
	path := fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && echo 7`)
	for _, event := range []string{"session-start", "stop"} {
		code, out, _ := shimEvent(t, event, adoptedDir(t), path, "{}")
		if msg := stopMessage(t, code, out); !strings.Contains(msg, "protocol 3") || !strings.Contains(msg, "speaks 7") {
			t.Fatalf("%s: message %q does not name both protocols", event, msg)
		}
	}
}

func TestShimStopMessageSurvivesHostileProtocolOutput(t *testing.T) {
	path := fakeAssure(t, `[ "$1 $2" = "hook protocol" ] && printf '"\n{\\'`)
	code, out, _ := shim(t, adoptedDir(t), path, "{}")
	if msg := stopMessage(t, code, out); !strings.Contains(msg, "speaks unknown") {
		t.Fatalf("message %q", msg)
	}
}
