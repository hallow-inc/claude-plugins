package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	StopCap          = 3
	stopStateVersion = 1
	maxFingerprints  = 32
)

type StopAction int

const (
	StopAllow StopAction = iota
	StopBlock
	StopEscalate
	StopOutsideReach
)

type FingerprintBlocks struct {
	Fingerprint string `json:"fingerprint"`
	Blocks      int    `json:"blocks"`
}

type StopState struct {
	Version      int                 `json:"version"`
	Blocks       int                 `json:"blocks"`
	Fingerprints []FingerprintBlocks `json:"fingerprints,omitempty"`
}

type StopCheck struct {
	Passed       bool
	OutsideReach bool
	Fingerprint  string
}

func (s StopState) with(fp string, blocks int) StopState {
	fs := slices.DeleteFunc(slices.Clone(s.Fingerprints), func(f FingerprintBlocks) bool { return f.Fingerprint == fp })
	fs = append(fs, FingerprintBlocks{Fingerprint: fp, Blocks: blocks})
	if len(fs) > maxFingerprints {
		fs = fs[len(fs)-maxFingerprints:]
	}
	s.Fingerprints = fs
	return s
}

func (s StopState) blocksFor(fp string) int {
	for _, f := range s.Fingerprints {
		if f.Fingerprint == fp {
			return f.Blocks
		}
	}
	return 0
}

func NextStop(s StopState, stopHookActive bool, c StopCheck) (StopState, StopAction) {
	s.Version = stopStateVersion
	n := s.blocksFor(c.Fingerprint)
	switch {
	case c.Passed:
		return StopState{Version: stopStateVersion}, StopAllow
	case c.OutsideReach:
		return s, StopOutsideReach
	case n >= StopCap:
		return s, StopEscalate
	}
	if !stopHookActive {
		s.Blocks = 0
	}
	if s.Blocks < StopCap {
		s.Blocks++
		return s.with(c.Fingerprint, n+1), StopBlock
	}
	s.Blocks = 0
	return s.with(c.Fingerprint, StopCap), StopEscalate
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
