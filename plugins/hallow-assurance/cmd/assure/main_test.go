package main

import (
	"bytes"
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

func TestUnknownCommandFails(t *testing.T) {
	known := map[string]bool{"help": true, "--help": true, "-h": true, "version": true, "--version": true}
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

func TestNoArgsFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code == 0 {
		t.Fatal("no-arg invocation exited 0")
	}
}
