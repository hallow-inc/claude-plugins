package adapterproto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	DescribeTimeout  = 2 * time.Second
	ClassifyTimeout  = 30 * time.Second
	ReferenceTimeout = 2 * time.Second
	ToolsTimeout     = 30 * time.Second
	MaxReference     = 256 << 10
	maxStdout        = 16 << 20
	maxStderr        = 64 << 10
	maxArgBytes      = 128 << 10
)

type Objective struct {
	Tool string `json:"tool"`
	Fast bool   `json:"fast"`
}

type Describe struct {
	Languages  []string             `json:"languages"`
	Claims     []string             `json:"claims"`
	Patterns   map[string][]string  `json:"patterns"`
	Objectives map[string]Objective `json:"objectives"`
	Reference  string               `json:"reference,omitempty"`
}

type Evidence struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type Run struct {
	Evidence     []Evidence        `json:"evidence"`
	ToolVersions map[string]string `json:"tool_versions"`
	PinnedBy     map[string]string `json:"pinned_by"`
	Environment  *Environment      `json:"environment"`
}

type Environment struct {
	Message string `json:"message"`
}

type Tool struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	PinnedBy string `json:"pinned_by"`
	Install  string `json:"install"`
}

type Tools struct {
	Tools       []Tool       `json:"tools"`
	Environment *Environment `json:"environment"`
}

type File struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Role     string `json:"role"`
}

type Error struct {
	Adapter string
	Sub     string
	Err     error
}

func (e *Error) Error() string { return e.Adapter + " " + e.Sub + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type unstartable struct{ error }

func (u unstartable) Unwrap() []error { return []error{u.error, core.ErrAdapterUnstartable} }

func startFailed(err error) bool {
	if _, exited := errors.AsType[*exec.ExitError](err); exited {
		return false
	}
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, exec.ErrNotFound)
}

func Executable(lang string) string { return "assure-adapter-" + lang }

func Resolve(lang string) (string, error) {
	path, err := exec.LookPath(Executable(lang))
	if err != nil {
		return "", &Error{Adapter: Executable(lang), Sub: "lookup", Err: unstartable{fmt.Errorf("not found on PATH: %w", err)}}
	}
	return path, nil
}

var errOverflow = errors.New("output exceeds cap")

type capped struct {
	buf      bytes.Buffer
	max      int
	stop     bool
	overflow bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		n, _ := c.buf.Write(p[:max(room, 0)])
		c.overflow = true
		if c.stop {
			return n, errOverflow
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func invoke(exe, dir, name, sub string, timeout time.Duration, args ...string) ([]byte, error) {
	return invokeCapped(exe, dir, name, sub, timeout, maxStdout, args...)
}

func invokeCapped(exe, dir, name, sub string, timeout time.Duration, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append([]string{sub}, args...)...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	stdout, stderr := &capped{max: limit, stop: true}, &capped{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	fail := func(format string, a ...any) error {
		return &Error{Adapter: name, Sub: sub, Err: fmt.Errorf(format, a...)}
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fail("killed after timeout of %s", timeout)
	case startFailed(err):
		return nil, &Error{Adapter: name, Sub: sub, Err: unstartable{err}}
	case stdout.overflow:
		return nil, fail("response exceeds %d bytes", limit)
	case err != nil:
		return nil, fail("%w; stderr: %s", err, strings.TrimSpace(stderr.buf.String()))
	}
	return stdout.buf.Bytes(), nil
}

var protocols = map[schemas.Kind]int{
	schemas.AdapterDescribe: 1,
	schemas.AdapterClassify: 0,
	schemas.AdapterRun:      1,
	schemas.AdapterTools:    1,
}

func protocolOf(out []byte) (int64, bool) {
	var env struct {
		Protocol *json.Number `json:"protocol"`
	}
	if json.Unmarshal(out, &env) != nil || env.Protocol == nil {
		return 0, false
	}
	got, err := env.Protocol.Int64()
	return got, err == nil
}

func protocolMismatch(k schemas.Kind, out []byte) error {
	got, ok := protocolOf(out)
	want := protocols[k]
	if !ok || got == int64(want) {
		return nil
	}
	return fmt.Errorf("protocol mismatch: received protocol %d, expected %d; install assure and its adapters from the same release", got, want)
}

func conform(name, sub string, k schemas.Kind, out []byte, v any) error {
	if err := protocolMismatch(k, out); err != nil {
		return &Error{Adapter: name, Sub: sub, Err: err}
	}
	vs, err := schemas.Validate(k, out)
	if err == nil && len(vs) > 0 {
		msgs := make([]string, len(vs))
		for i, x := range vs {
			msgs[i] = x.String()
		}
		err = errors.New("response violates the protocol: " + strings.Join(msgs, "; "))
	}
	if err == nil {
		err = json.Unmarshal(out, v)
	}
	if err != nil {
		return &Error{Adapter: name, Sub: sub, Err: err}
	}
	return nil
}

func RunDescribe(exe, dir, lang string) (Describe, json.RawMessage, error) {
	name := Executable(lang)
	out, err := invoke(exe, dir, name, "describe", DescribeTimeout)
	if err != nil {
		return Describe{}, nil, err
	}
	var d Describe
	if err := conform(name, "describe", schemas.AdapterDescribe, out, &d); err != nil {
		return Describe{}, nil, err
	}
	if !slices.Contains(d.Languages, lang) {
		return Describe{}, nil, &Error{Adapter: name, Sub: "describe", Err: fmt.Errorf("languages %v does not include %q", d.Languages, lang)}
	}
	if d.Reference != "" && !slices.Contains(d.Languages, d.Reference) {
		return Describe{}, nil, &Error{Adapter: name, Sub: "describe", Err: fmt.Errorf("response violates the protocol: /reference: %q is not one of languages %v", d.Reference, d.Languages)}
	}
	return d, json.RawMessage(bytes.TrimSpace(out)), nil
}

func Classify(dir, lang string, paths []string) ([]File, error) {
	exe, err := Resolve(lang)
	if err != nil {
		return nil, err
	}
	name := Executable(lang)
	var files []File
	for len(paths) > 0 {
		n, size := 0, 0
		for n < len(paths) && (n == 0 || size+len(paths[n])+1 <= maxArgBytes) {
			size += len(paths[n]) + 1
			n++
		}
		out, err := invoke(exe, dir, name, "classify", ClassifyTimeout, paths[:n]...)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Files []File `json:"files"`
		}
		if err := conform(name, "classify", schemas.AdapterClassify, out, &resp); err != nil {
			return nil, err
		}
		files = append(files, resp.Files...)
		paths = paths[n:]
	}
	return files, nil
}

func RunObjective(dir, lang, objective, ref, out string, timeout time.Duration) (Run, error) {
	exe, err := Resolve(lang)
	if err != nil {
		return Run{}, err
	}
	name := Executable(lang)
	raw, err := invoke(exe, dir, name, "run", timeout, objective, "--changed-from", ref, "--out", out)
	if err != nil {
		return Run{}, err
	}
	var r Run
	if err := conform(name, "run", schemas.AdapterRun, raw, &r); err != nil {
		return Run{}, err
	}
	for _, tool := range slices.Sorted(maps.Keys(r.PinnedBy)) {
		if _, ok := r.ToolVersions[tool]; !ok {
			return Run{}, &Error{Adapter: name, Sub: "run", Err: fmt.Errorf("response violates the protocol: /pinned_by: %q has no tool_versions entry", tool)}
		}
	}
	return r, nil
}

func RunTools(dir, lang string) (Tools, error) {
	exe, err := Resolve(lang)
	if err != nil {
		return Tools{}, err
	}
	name := Executable(lang)
	raw, err := invoke(exe, dir, name, "tools", ToolsTimeout, "--root", dir)
	if err != nil {
		return Tools{}, err
	}
	var t Tools
	if err := conform(name, "tools", schemas.AdapterTools, raw, &t); err != nil {
		return Tools{}, err
	}
	seen := map[string]bool{}
	for _, tool := range t.Tools {
		if seen[tool.Name] {
			return Tools{}, &Error{Adapter: name, Sub: "tools", Err: fmt.Errorf("response violates the protocol: /tools: duplicate tool name %q", tool.Name)}
		}
		seen[tool.Name] = true
	}
	return t, nil
}

func Reference(dir, lang string) ([]byte, error) {
	exe, err := Resolve(lang)
	if err != nil {
		return nil, err
	}
	name := Executable(lang)
	out, err := invokeCapped(exe, dir, name, "reference", ReferenceTimeout, MaxReference)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, &Error{Adapter: name, Sub: "reference", Err: errors.New("empty output")}
	}
	return out, nil
}
