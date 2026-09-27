package adapterproto

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func countingAdapter(t *testing.T, root string) string {
	t.Helper()
	return fakeAdapter(t, "go", "echo run >> '"+filepath.Join(root, "runs")+"'; echo '"+goodDescribe+"'")
}

func runs(t *testing.T, root string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "runs"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(data) / len("run\n")
}

func mustDescribe(t *testing.T, root string) {
	t.Helper()
	ds, errs := Descriptions(root, []string{"go"})
	if len(errs) > 0 || ds["go"].Claims[0] != "**/*.go" {
		t.Fatalf("descriptions %v errors %v", ds, errs)
	}
}

func TestFreshCacheStartsNoProcess(t *testing.T) {
	root := t.TempDir()
	countingAdapter(t, root)
	mustDescribe(t, root)
	mustDescribe(t, root)
	mustDescribe(t, root)
	if n := runs(t, root); n != 1 {
		t.Fatalf("adapter ran %d times; a fresh cache must serve every call after the first", n)
	}
	data, err := os.ReadFile(filepath.Join(root, CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.AdapterCache, data); err != nil || len(vs) > 0 {
		t.Fatalf("written cache is invalid: %v %v", err, vs)
	}
}

func TestChangedExecutableTriggersRefresh(t *testing.T) {
	root := t.TempDir()
	exe := countingAdapter(t, root)
	mustDescribe(t, root)
	f, err := os.OpenFile(exe, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# upgraded\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustDescribe(t, root)
	if n := runs(t, root); n != 2 {
		t.Fatalf("adapter ran %d times; a changed binary must be described again", n)
	}
}

func TestCorruptCacheIsRebuilt(t *testing.T) {
	root := t.TempDir()
	countingAdapter(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".assure/state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CacheFile), []byte(`{"version":0,"adapters":{"go":{"path":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mustDescribe(t, root)
	mustDescribe(t, root)
	if n := runs(t, root); n != 1 {
		t.Fatalf("adapter ran %d times; want one rebuild then cache hits", n)
	}
}

func TestFailedRefreshLeavesNoUsableEntry(t *testing.T) {
	root := t.TempDir()
	exe := countingAdapter(t, root)
	mustDescribe(t, root)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ds, errs := Descriptions(root, []string{"go"})
		if _, ok := ds["go"]; ok || errs["go"] == nil {
			t.Fatalf("broken adapter still produced a description: %v %v", ds, errs)
		}
	}
}

func TestMissingAdapterIsAnErrorEvenWithCache(t *testing.T) {
	root := t.TempDir()
	countingAdapter(t, root)
	mustDescribe(t, root)
	t.Setenv("PATH", t.TempDir())
	if ds, errs := Descriptions(root, []string{"go"}); len(ds) != 0 || errs["go"] == nil {
		t.Fatalf("cache served a language whose adapter is gone: %v %v", ds, errs)
	}
}
