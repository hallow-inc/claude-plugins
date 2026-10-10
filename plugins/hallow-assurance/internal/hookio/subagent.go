package hookio

import (
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

var roleAgents = map[string]bool{core.Verifier: true}

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
	next, resp := decideStop(stopInput{name: "verifier check", rep: app.VerifierCheck(m, "HEAD", in.AgentID), active: active}, state)
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

func manifestUnavailable(who string, err error) response {
	return response{out: &output{SystemMessage: "assure: the " + who + " check cannot run, because the manifest does not load, and the agent may not edit it; the agent was allowed to stop: " + err.Error()}}
}
