package core

type Status string

const (
	Pass         Status = "pass"
	Fail         Status = "fail"
	AdvisoryFail Status = "advisory-fail"
)

type Finding struct {
	Level   Level
	Located bool
	Text    string
}

type Evidence struct {
	Problems []string
	Failing  []string
	Findings []Finding
}

type Outcome struct {
	ID      string
	Status  Status
	Details []string
}

func changedStatus(o Objective, changed []Level) (req, adv bool) {
	for _, l := range changed {
		switch o.Levels[l] {
		case "required":
			req = true
		case "advisory":
			adv = true
		}
	}
	return req, adv
}

func DecideFast(o Objective, changed []Level, ev Evidence) Outcome {
	req, adv := changedStatus(o, changed)
	out := Outcome{ID: o.ID, Status: Pass}
	generic := len(ev.Problems) > 0 || len(ev.Failing) > 0
	out.Details = append(append(out.Details, ev.Problems...), ev.Failing...)
	blocking := req && generic
	advisory := generic && adv
	for _, f := range ev.Findings {
		out.Details = append(out.Details, f.Text)
		if !f.Located {
			blocking = blocking || req
			advisory = advisory || adv
			continue
		}
		switch o.Levels[f.Level] {
		case "required":
			blocking = blocking || req
			advisory = true
		case "advisory":
			advisory = true
		}
	}
	switch {
	case blocking:
		out.Status = Fail
	case advisory:
		out.Status = AdvisoryFail
	}
	return out
}
