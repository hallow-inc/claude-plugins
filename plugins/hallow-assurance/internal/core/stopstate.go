package core

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const StopCap = 3

type StopAction int

const (
	StopAllow StopAction = iota
	StopBlock
	StopEscalate
)

type StopState struct {
	Version int `json:"version"`
	Blocks  int `json:"blocks"`
}

func NextStop(s StopState, stopHookActive, passed bool) (StopState, StopAction) {
	if !stopHookActive {
		s.Blocks = 0
	}
	switch {
	case passed:
		return StopState{}, StopAllow
	case s.Blocks < StopCap:
		return StopState{Blocks: s.Blocks + 1}, StopBlock
	default:
		return StopState{}, StopEscalate
	}
}

func stopStatePath(root, session string) string {
	return filepath.Join(root, StateDir, "stop-"+session+".json")
}

func ReadStopState(root, session string) StopState {
	data, err := os.ReadFile(stopStatePath(root, session))
	if err != nil {
		return StopState{}
	}
	if vs, err := schemas.Validate(schemas.StopState, data); err != nil || len(vs) > 0 {
		return StopState{}
	}
	var s StopState
	if json.Unmarshal(data, &s) != nil {
		return StopState{}
	}
	return s
}

func WriteStopState(root, session string, s StopState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeState(stopStatePath(root, session), data)
}
