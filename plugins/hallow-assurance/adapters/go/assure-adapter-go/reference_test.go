package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestReferencePrintsEmbeddedFileWithoutASourceTree(t *testing.T) {
	want, err := os.ReadFile("reference.md")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"reference"}, &out, &errb); code != 0 || errb.Len() > 0 {
		t.Fatalf("reference exited %d: %s", code, errb.String())
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("stdout (%d bytes) differs from reference.md (%d bytes)", out.Len(), len(want))
	}
	if !bytes.HasPrefix(want, []byte("# ")) || len(want) >= 256<<10 {
		t.Fatalf("reference.md must be Markdown starting with \"# \" and under 256 KiB, got %d bytes", len(want))
	}
	for _, topic := range []string{"pgregory.net/rapid", "testdata/fuzz/", "go test -race -shuffle=on", "Gremlins", "VER-MUTATION-CHANGED", "Assure-Kind: fix"} {
		if !strings.Contains(string(want), topic) {
			t.Errorf("reference.md does not cover %q", topic)
		}
	}
}
