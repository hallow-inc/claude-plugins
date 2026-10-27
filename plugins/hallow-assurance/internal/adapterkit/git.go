package adapterkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func GitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func GitPaths(dir string, args ...string) ([]string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("git not found on PATH")
	}
	out, err := GitBytes(dir, append([]string{args[0], "-z"}, args[1:]...)...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func ChangedPaths(root, ref string) ([]string, error) {
	diff, err := GitPaths(root, "diff", "--name-only", "--relative", ref, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := GitPaths(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return append(diff, untracked...), nil
}
