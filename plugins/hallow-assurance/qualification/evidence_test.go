package qualification

import (
	"maps"
	"slices"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
)

var emptyDocs = map[string][]byte{
	"lint.sarif":          []byte(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"golangci-lint"}},"results":[]}]}` + "\n"),
	"test.junit":          []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<testsuites></testsuites>\n"),
	"test.fail_on_base":   []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<testsuites></testsuites>\n"),
	"fuzz.run":            []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<testsuites></testsuites>\n"),
	"mutation.report":     []byte(`{"schemaVersion":"1","thresholds":{"high":80,"low":60},"files":{}}` + "\n"),
	"coverage.resolution": {},
}

func junitResults(data []byte) (int, error) {
	j, err := evidence.ParseJUnit(data)
	return j.Cases + len(j.Suites) + len(j.Failing) + len(j.Passed) + len(j.Skips), err
}

var evidenceReaders = map[string]func([]byte) (int, error){
	"lint.sarif": func(data []byte) (int, error) {
		rs, err := evidence.ParseSARIF(data)
		return len(rs), err
	},
	"test.junit":        junitResults,
	"test.fail_on_base": junitResults,
	"fuzz.run":          junitResults,
	"mutation.report": func(data []byte) (int, error) {
		ms, err := evidence.ParseMutationReport(data)
		return len(ms), err
	},
	"coverage.resolution": func(data []byte) (int, error) {
		fs, err := evidence.ParseLCOV(data)
		return len(fs), err
	},
}

func TestEmptyDocsParseWithNoResults(t *testing.T) {
	if !slices.Equal(slices.Sorted(maps.Keys(emptyDocs)), slices.Sorted(maps.Keys(evidenceReaders))) {
		t.Fatalf("empty documents for %q, readers for %q: every kind needs both", slices.Sorted(maps.Keys(emptyDocs)), slices.Sorted(maps.Keys(evidenceReaders)))
	}
	for kind, doc := range emptyDocs {
		n, err := evidenceReaders[kind](doc)
		if err != nil {
			t.Errorf("%s: assure rejects the empty document: %v", kind, err)
		}
		if n != 0 {
			t.Errorf("%s: the empty document yields %d results, want 0", kind, n)
		}
	}
}
