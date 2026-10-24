package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestKnownCommandsSucceed(t *testing.T) {
	for _, cmd := range []string{"help", "--help", "-h", "version", "--version"} {
		var out, errOut bytes.Buffer
		if code := run([]string{cmd}, &out, &errOut); code != 0 {
			t.Errorf("%s: exit %d, stderr %q", cmd, code, errOut.String())
		}
		if out.Len() == 0 {
			t.Errorf("%s: no output", cmd)
		}
	}
}

func stubBuildInfo(t *testing.T, mainVersion string, ok bool) {
	t.Helper()
	saved := buildInfo
	t.Cleanup(func() { buildInfo = saved })
	buildInfo = func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance", Version: mainVersion}}, true
	}
}

func hookProtocolOutput(t *testing.T) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := run([]string{"hook", "protocol"}, &out, &errOut); code != 0 {
		t.Fatalf("assure hook protocol: exit %d %q", code, errOut.String())
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func checkVersionLine(t fataler, want string) {
	t.Helper()
	for _, cmd := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		code := run([]string{cmd}, &out, &errOut)
		if code != 0 || out.String() != want || errOut.Len() != 0 {
			t.Fatalf("%s: exit %d stdout %q stderr %q; want exit 0 and exactly %q, so a user can tell which release and hook protocol a binary speaks", cmd, code, out.String(), errOut.String(), want)
		}
	}
}

func TestVersionPrintsBuildVersionAndHookProtocol(t *testing.T) {
	n := hookProtocolOutput(t)
	rapid.Check(t, func(rt *rapid.T) {
		v := rapid.OneOf(
			rapid.StringMatching(`v(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})`),
			rapid.StringMatching(`v0\.0\.0-2026[01][0-9][0-3][0-9][0-2][0-9][0-5][0-9][0-5][0-9]-[0-9a-f]{12}(\+dirty)?`),
			rapid.Just("(devel)"),
		).Draw(rt, "mainVersion")
		saved := buildInfo
		defer func() { buildInfo = saved }()
		buildInfo = func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
		}
		checkVersionLine(rt, v+" (hook protocol "+n+")\n")
	})
}

func TestVersionOfReleasedBinary(t *testing.T) {
	stubBuildInfo(t, "v0.2.0", true)
	checkVersionLine(t, "v0.2.0 (hook protocol 3)\n")
}

func TestVersionOfDevelBuild(t *testing.T) {
	stubBuildInfo(t, "(devel)", true)
	checkVersionLine(t, "(devel) (hook protocol 3)\n")
}

func TestVersionWithoutBuildInfoIsDevel(t *testing.T) {
	stubBuildInfo(t, "", false)
	checkVersionLine(t, "(devel) (hook protocol "+hookProtocolOutput(t)+")\n")
}

func TestUnknownCommandFails(t *testing.T) {
	known := map[string]bool{"help": true, "--help": true, "-h": true, "version": true, "--version": true, "context": true, "classify": true,
		"check": true, "evaluate": true, "hook": true, "reference": true, "tools": true}
	rapid.Check(t, func(t *rapid.T) {
		cmd := rapid.String().Filter(func(s string) bool { return !known[s] }).Draw(t, "cmd")
		var out, errOut bytes.Buffer
		if code := run([]string{cmd}, &out, &errOut); code == 0 {
			t.Fatalf("command %q exited 0", cmd)
		}
		if !strings.Contains(errOut.String(), "unknown command") {
			t.Fatalf("stderr lacks diagnosis: %q", errOut.String())
		}
	})
}

func TestRetiredBaselineCommandIsUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"baseline"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("assure baseline: exit %d, stderr %q; the baseline ratcheted lint findings that no objective produces now, so the command is gone and must exit 2 as unknown", code, errOut.String())
	}
	var help bytes.Buffer
	run([]string{"--help"}, &help, &help)
	if strings.Contains(help.String(), "baseline") {
		t.Fatalf("usage still offers baseline:\n%s", help.String())
	}
}

func TestRemovedCommandsAreUnknown(t *testing.T) {
	var help bytes.Buffer
	run([]string{"--help"}, &help, &help)
	for _, cmd := range []string{"guard", "record", "reviews"} {
		var out, errOut bytes.Buffer
		if code := run([]string{cmd}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown command") {
			t.Errorf("assure %s: exit %d, stderr %q; the guard, provenance, and reviews are removed, so the command must exit 2 as unknown", cmd, code, errOut.String())
		}
		if strings.Contains(help.String(), "  "+cmd+" ") {
			t.Errorf("usage still offers %s:\n%s", cmd, help.String())
		}
	}
}

func TestNoArgsFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code == 0 {
		t.Fatal("no-arg invocation exited 0")
	}
}
