package adapterkit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func asciiSpace() *rapid.Generator[string] {
	return rapid.StringOfN(rapid.SampledFrom([]rune{' ', '\t', '\n', '\r', '\v', '\f'}), 0, 6, -1)
}

func TestToolOutputReportsMissingToolByName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		name := rapid.StringMatching(`[a-z][a-z0-9_-]{0,12}`).Draw(rt, "name")
		args := rapid.SliceOfN(rapid.StringMatching(`[a-z-]{0,6}`), 0, 3).Draw(rt, "args")
		out, err := ToolOutput(dir, name, args...)
		want := name + " not found on PATH"
		if err == nil || err.Error() != want || out != "" {
			rt.Fatalf("ToolOutput(%q) with an empty PATH = %q, %v; want error %q so adapters tell the user which tool is missing instead of recording an empty version", name, out, err, want)
		}
	})
}

func TestToolOutputRunsInDir(t *testing.T) {
	base := realTempDir(t)
	rapid.Check(t, func(rt *rapid.T) {
		sub := rapid.StringMatching(`[aZ\-é日]([aZ\-é日 ]{0,6}[aZ\-é日])?`).Draw(rt, "sub")
		dir := filepath.Join(base, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			rt.Fatalf("%v", err)
		}
		got, err := ToolOutput(dir, "sh", "-c", "pwd -P")
		if err != nil {
			rt.Fatalf("ToolOutput: %v", err)
		}
		if got != dir {
			rt.Fatalf("ToolOutput(%q, pwd) ran in %q; a version or toplevel query run outside dir reports another project's evidence", dir, got)
		}
	})
}

func TestToolOutputTrimsStdoutAndExcludesStderr(t *testing.T) {
	dir := t.TempDir()
	body := []rune{'a', 'Z', '0', '%', '\\', '"', '\'', 'é', '日', ' ', '\t', '\n'}
	edge := []rune{'a', 'Z', '0', '%', '\\', '"', 'é', '日'}
	rapid.Check(t, func(rt *rapid.T) {
		content := ""
		if rapid.Bool().Draw(rt, "nonEmpty") {
			content = string(rapid.SampledFrom(edge).Draw(rt, "first")) +
				rapid.StringOfN(rapid.SampledFrom(body), 0, 10, -1).Draw(rt, "middle") +
				string(rapid.SampledFrom(edge).Draw(rt, "last"))
		}
		stdout := asciiSpace().Draw(rt, "lead") + content + asciiSpace().Draw(rt, "trail")
		stderr := rapid.StringMatching(`[a-z]{1,8}`).Draw(rt, "stderr")

		got, err := ToolOutput(dir, "sh", "-c", `printf '%s' "$1"; printf '%s' "$2" >&2`, "sh", stdout, stderr)
		if err != nil {
			rt.Fatalf("ToolOutput: %v", err)
		}
		if got != content {
			rt.Fatalf("ToolOutput printing %q (stderr %q) = %q, want %q; callers compare versions and SHAs as exact strings, so untrimmed whitespace or stderr text breaks the match", stdout, stderr, got, content)
		}
	})
}

func TestToolOutputFailureNamesCommand(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		code := rapid.IntRange(1, 125).Draw(rt, "code")
		extra := rapid.SliceOfN(rapid.StringOfN(rapid.SampledFrom([]rune{'a', ' ', '-', 'é', '\t'}), 0, 5, -1), 0, 3).Draw(rt, "extra")
		args := append([]string{"-c", `printf partial; exit "$1"`, "sh", strconv.Itoa(code)}, extra...)

		out, err := ToolOutput(dir, "sh", args...)
		if err == nil || out != "" {
			rt.Fatalf("ToolOutput(sh %q) exiting %d = %q, %v; want empty output and an error, since partial output from a failed command is not evidence", args, code, out, err)
		}
		prefix := "sh " + strings.Join(args, " ") + ": "
		if !strings.HasPrefix(err.Error(), prefix) {
			rt.Fatalf("error = %q, want prefix %q so the user sees which command failed", err, prefix)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != code {
			rt.Fatalf("error = %v, want it to wrap the *exec.ExitError with code %d", err, code)
		}
	})
}
