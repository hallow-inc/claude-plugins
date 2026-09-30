package adapterproto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	DescribeTimeout = 2 * time.Second
	ClassifyTimeout = 30 * time.Second
	maxStdout       = 16 << 20
	maxStderr       = 64 << 10
	maxArgBytes     = 128 << 10
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
}

type Evidence struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type Run struct {
	Evidence     []Evidence        `json:"evidence"`
	ToolVersions map[string]string `json:"tool_versions"`
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

type capped struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:max(room, 0)])
		c.overflow = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func invoke(exe, dir, name, sub string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append([]string{sub}, args...)...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	stdout, stderr := &capped{max: maxStdout}, &capped{max: maxStderr}
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
	case err != nil:
		return nil, fail("%w; stderr: %s", err, strings.TrimSpace(stderr.buf.String()))
	case stdout.overflow:
		return nil, fail("response exceeds %d bytes", maxStdout)
	}
	return stdout.buf.Bytes(), nil
}

func conform(name, sub string, k schemas.Kind, out []byte, v any) error {
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
	return r, nil
}
