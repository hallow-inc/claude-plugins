package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterproto"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const VerifierObjective = "VER-TESTS-PASS"

const (
	sarifOpen  = "```sarif"
	sarifClose = "```"
)

var InspectionLevels = []string{"error", "warning", "note"}

func VerifierCheck(m *core.Manifest, ref, label string) Report {
	return check(m, ref, label, func(id string, _ adapterproto.Objective) bool { return id == VerifierObjective })
}

func ExtractSARIF(text string) ([]byte, error) {
	lines := strings.SplitAfter(text, "\n")
	var blocks [][]byte
	offset := 0
	start, openLine := -1, 0
	for i, l := range lines {
		bare := strings.TrimSuffix(strings.TrimSuffix(l, "\n"), "\r")
		if start < 0 && bare == sarifOpen {
			start, openLine = offset+len(l), i+1
		} else if start >= 0 && bare == sarifClose {
			body := strings.TrimSuffix(strings.TrimSuffix(text[start:offset], "\n"), "\r")
			blocks = append(blocks, []byte(body))
			start = -1
		}
		offset += len(l)
	}
	if start >= 0 {
		return nil, fmt.Errorf("the %s block opened on line %d is never closed by a line that is exactly %s", sarifOpen, openLine, sarifClose)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no %s block was found", sarifOpen)
	}
	if len(blocks) > 1 {
		return nil, fmt.Errorf("%d %s blocks were found; exactly one is required", len(blocks), sarifOpen)
	}
	return blocks[0], nil
}

func ValidateInspection(data []byte) (map[string]int, []string, error) {
	vs, err := schemas.Validate(schemas.Inspection, data)
	if err != nil {
		return nil, nil, err
	}
	if len(vs) > 0 {
		msgs := make([]string, len(vs))
		for i, v := range vs {
			msgs[i] = v.String()
		}
		return nil, msgs, nil
	}
	var log struct {
		Runs []struct {
			Results []struct {
				Level string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, nil, err
	}
	counts := map[string]int{}
	for _, run := range log.Runs {
		for _, r := range run.Results {
			counts[r.Level]++
		}
	}
	return counts, nil, nil
}

func RenderCounts(counts map[string]int) string {
	parts := make([]string, len(InspectionLevels))
	for i, l := range InspectionLevels {
		parts[i] = fmt.Sprintf("%d %s", counts[l], l)
	}
	return strings.Join(parts, ", ")
}

func Inspect(text string) ([]byte, map[string]int, error) {
	data, err := ExtractSARIF(text)
	if err != nil {
		return nil, nil, err
	}
	counts, vs, err := ValidateInspection(data)
	if err != nil {
		return nil, nil, fmt.Errorf("the %s block is not JSON: %w", sarifOpen, err)
	}
	if len(vs) > 0 {
		if len(vs) > 5 {
			vs = append(vs[:5], fmt.Sprintf("… %d more", len(vs)-5))
		}
		return nil, nil, fmt.Errorf("the %s block violates the inspection profile:\n  - %s", sarifOpen, strings.Join(vs, "\n  - "))
	}
	return data, counts, nil
}
