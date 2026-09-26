package catalog

import (
	"bytes"
	"os"
	"testing"
)

func TestEmbeddedCopyIsTheReviewedFile(t *testing.T) {
	onDisk, err := os.ReadFile("v0/objectives.yaml")
	if err != nil {
		t.Fatal(err)
	}
	embedded, ok := Version("v0")
	if !ok || !bytes.Equal(embedded, onDisk) {
		t.Fatal("embedded v0 differs from catalog/v0/objectives.yaml")
	}
}

func TestUnknownVersionIsAbsent(t *testing.T) {
	for _, v := range []string{"v9", "", "../catalog", "v0/../v0"} {
		if _, ok := Version(v); ok {
			t.Errorf("Version(%q) found a catalog", v)
		}
	}
}
