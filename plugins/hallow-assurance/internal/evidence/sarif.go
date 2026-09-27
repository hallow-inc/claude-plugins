package evidence

import (
	"encoding/json"
	"fmt"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type Result struct {
	RuleID  string
	Level   string
	Message string
	URI     string
}

type sarifLog struct {
	Runs []struct {
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

func ParseSARIF(data []byte) ([]Result, error) {
	vs, err := schemas.Validate(schemas.AdapterLint, data)
	if err != nil {
		return nil, fmt.Errorf("sarif: %w", err)
	}
	if len(vs) > 0 {
		return nil, fmt.Errorf("sarif: %s", vs[0])
	}
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("sarif: %w", err)
	}
	out := []Result{}
	for _, run := range log.Runs {
		for _, r := range run.Results {
			res := Result{RuleID: r.RuleID, Level: r.Level, Message: r.Message.Text}
			if res.Level == "" {
				res.Level = "warning"
			}
			if len(r.Locations) > 0 {
				res.URI = r.Locations[0].PhysicalLocation.ArtifactLocation.URI
			}
			out = append(out, res)
		}
	}
	return out, nil
}
