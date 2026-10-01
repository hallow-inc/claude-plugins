package evidence

import (
	"encoding/json"
	"fmt"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type BudgetFile struct {
	Path         string `json:"path"`
	Role         string `json:"role"`
	AddedCases   int    `json:"added_cases"`
	ChangedLines int    `json:"changed_lines"`
}

type testBudget struct {
	Files []BudgetFile `json:"files"`
}

func ParseTestBudget(data []byte) ([]BudgetFile, error) {
	vs, err := schemas.Validate(schemas.TestBudget, data)
	if err != nil {
		return nil, fmt.Errorf("test budget: %w", err)
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("test budget: %s", vs[0])
	}
	var tb testBudget
	if err := json.Unmarshal(data, &tb); err != nil {
		return nil, fmt.Errorf("test budget: %w", err)
	}
	return tb.Files, nil
}
