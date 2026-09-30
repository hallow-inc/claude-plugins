package evidence

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestSARIFResultsRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var want []Result
		var runs []any
		for range rapid.IntRange(1, 2).Draw(t, "nruns") {
			results := []any{}
			for range rapid.IntRange(0, 4).Draw(t, "nresults") {
				r := Result{
					RuleID:  rapid.StringMatching(`[a-z]{1,8}`).Draw(t, "rule"),
					Level:   rapid.SampledFrom([]string{"", "none", "note", "warning", "error"}).Draw(t, "level"),
					Message: rapid.StringMatching(`[ -~]{1,20}`).Draw(t, "msg"),
				}
				doc := map[string]any{"ruleId": r.RuleID, "message": map[string]any{"text": r.Message}}
				if r.Level != "" {
					doc["level"] = r.Level
				} else {
					r.Level = "warning"
				}
				if rapid.Bool().Draw(t, "hasmetric") {
					r.Metric, r.HasMetric = float64(rapid.IntRange(0, 200).Draw(t, "metric")), true
					doc["properties"] = map[string]any{"metric": r.Metric, "other": "x"}
				} else if rapid.Bool().Draw(t, "otherprops") {
					doc["properties"] = map[string]any{"tags": []any{"x"}}
				}
				if rapid.Bool().Draw(t, "located") {
					r.URI = rapid.StringMatching(`[a-z]{1,6}(/[a-z]{1,6}){0,2}\.go`).Draw(t, "uri")
					doc["locations"] = []any{map[string]any{"physicalLocation": map[string]any{"artifactLocation": map[string]any{"uri": r.URI}}}}
				}
				results = append(results, doc)
				want = append(want, r)
			}
			runs = append(runs, map[string]any{"tool": map[string]any{"driver": map[string]any{"name": "lint"}}, "results": results})
		}
		data, _ := json.Marshal(map[string]any{"version": "2.1.0", "runs": runs})
		got, err := ParseSARIF(data)
		if err != nil {
			t.Fatalf("parse: %v\n%s", err, data)
		}
		if len(want) == 0 {
			want = []Result{}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}

func TestSARIFResultWithLocation(t *testing.T) {
	doc := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"vet"}},"results":[{"ruleId":"printf","level":"error","message":{"text":"bad"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"internal/core/guard.go"}}}]}]}]}`
	got, err := ParseSARIF([]byte(doc))
	if err != nil || len(got) != 1 || got[0].URI != "internal/core/guard.go" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSARIFWrongVersion(t *testing.T) {
	_, err := ParseSARIF([]byte(`{"version":"2.0.0","runs":[]}`))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("want version error, got %v", err)
	}
}

func TestSARIFMetric(t *testing.T) {
	doc := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"cyclo"}},"results":[{"ruleId":"cyclomatic","message":{"text":"f"},"properties":{"metric":16}}]}]}`
	got, err := ParseSARIF([]byte(doc))
	if err != nil || len(got) != 1 || !got[0].HasMetric || got[0].Metric != 16 {
		t.Fatalf("got %+v, %v; thresholds are applied to this metric", got, err)
	}
}

func TestSARIFNonNumericMetric(t *testing.T) {
	for _, m := range []string{`"16"`, `null`, `[16]`} {
		doc := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"cyclo"}},"results":[{"ruleId":"cyclomatic","message":{"text":"f"},"properties":{"metric":` + m + `}}]}]}`
		if _, err := ParseSARIF([]byte(doc)); err == nil || !strings.Contains(err.Error(), "metric") {
			t.Errorf("metric %s: want error, got %v; a non-numeric metric must not silently skip the threshold", m, err)
		}
	}
}
