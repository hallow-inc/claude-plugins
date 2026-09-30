package app

import (
	"errors"
	"strings"
	"sync"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func Baseline(m *core.Manifest) (core.Baseline, error) {
	changed, err := ChangedFiles(m.Root, EmptyTree)
	if err != nil {
		return core.Baseline{}, err
	}
	var rep EvalReport
	planned, _ := planEvaluate(m, changed, false, &rep)
	var jobs []job
	for _, j := range planned {
		if j.obj.Evidence == "lint.sarif" {
			jobs = append(jobs, j)
		}
	}
	evs := make([]core.Evidence, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Go(func() { evs[i], _ = collect(m, j, EmptyTree, EvaluateTimeout) })
	}
	wg.Wait()
	problems := rep.Problems
	var fps []core.Fingerprint
	for i, ev := range evs {
		problems = append(problems, ev.Problems...)
		o := jobs[i].obj
		for _, f := range ev.Findings {
			if f.Located && o.Levels[f.Level] != "" && !o.UnderThreshold(f) {
				fps = append(fps, core.Fingerprint{Objective: o.ID, Rule: f.Rule, Path: f.Path, Message: f.Message})
			}
		}
	}
	if len(problems) > 0 {
		return core.Baseline{}, errors.New(strings.Join(problems, "\n"))
	}
	return core.NewBaseline(fps), nil
}
