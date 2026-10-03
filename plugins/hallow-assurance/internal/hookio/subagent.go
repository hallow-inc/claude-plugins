package hookio

import (
	"fmt"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var roleAgents = map[string]bool{core.Verifier: true, core.Inspector: true}

const sarifContract = "End your final message with exactly one fenced block that opens with a line that is exactly ```sarif and closes with a line that is exactly ```, holding a SARIF 2.1.0 log with one run whose tool.driver.name is " + core.Inspector + ". Every result needs a non-empty ruleId, a level of error, warning, or note, a non-empty message.text, and a location with a repo-relative artifactLocation.uri and region.startLine ≥ 1. Use \"results\": [] when there are no findings."

func subagentStop(in input) response {
	m, ok, err := adopted(in.Cwd)
	if !ok {
		return response{}
	}
	if err != nil {
		return manifestUnavailable(in.AgentType, err)
	}
	state := core.ReadSubagentStopState(m.Root, in.SessionID, in.AgentID)
	active := state.Blocks > 0
	if in.StopHookActive != nil {
		active = *in.StopHookActive
	}
	var next core.StopState
	var resp response
	if in.AgentType == core.Verifier {
		next, resp = decideStop(stopInput{name: "verifier check", rep: app.VerifierCheck(m, "HEAD", in.AgentID), active: active}, state)
	} else {
		next, resp = inspectorStop(m, in, state, active)
	}
	if err := core.WriteSubagentStopState(m.Root, in.SessionID, in.AgentID, next); err != nil && resp.out != nil {
		o := *resp.out
		note := "\nwarning: could not record the retry count: " + err.Error() + "\n"
		if o.Decision == "block" {
			o.Reason += note
		} else {
			o.SystemMessage += note
		}
		resp.out = &o
	}
	return resp
}

func inspectorStop(m *core.Manifest, in input, state core.StopState, active bool) (core.StopState, response) {
	var problem string
	var data []byte
	var counts map[string]int
	if in.LastMessage == nil {
		problem = "the SubagentStop input has no last_assistant_message, so the inspector's report cannot be read; this Claude Code version may not send it"
	} else if d, c, err := app.Inspect(*in.LastMessage); err != nil {
		problem = err.Error()
	} else {
		data, counts = d, c
	}
	if problem == "" {
		next, _ := core.NextStop(state, active, core.StopCheck{Passed: true})
		if err := core.WriteInspection(m.Root, in.SessionID, in.AgentID, data); err != nil {
			return next, response{out: &output{SystemMessage: "assure: the inspector's SARIF is valid but could not be written; the inspector was allowed to stop: " + err.Error()}}
		}
		return next, response{out: &output{SystemMessage: fmt.Sprintf("assure: inspection written to %s (%s)",
			core.InspectionPath(".", in.SessionID, in.AgentID), app.RenderCounts(counts))}}
	}
	check := core.StopCheck{Fingerprint: core.StopFingerprint([]core.FailureKey{{Objective: "inspection", Keys: []string{"sarif"}}}, nil)}
	next, action := core.NextStop(state, active, check)
	switch action {
	case core.StopBlock:
		return next, response{out: &output{Decision: "block", Reason: fmt.Sprintf(
			"assure inspector check failed (attempt %d of %d): %s\n\n%s", next.Blocks, core.StopCap, problem, sarifContract)}, code: 2}
	default:
		return next, response{out: &output{SystemMessage: fmt.Sprintf(
			"assure: the inspector produced no valid SARIF after %d attempts and was allowed to stop; no inspection was written: %s", core.StopCap, problem)}}
	}
}

func manifestUnavailable(who string, err error) response {
	return response{out: &output{SystemMessage: "assure: the " + who + " check cannot run, because the manifest does not load, and the agent may not edit it; the agent was allowed to stop: " + err.Error()}}
}
