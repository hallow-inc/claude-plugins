package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func AddedLines(root, ref string) (map[string][]int, error) {
	cmd := exec.CommandContext(context.Background(), "git", "-c", "core.quotePath=false", "diff", "-U0", "--no-ext-diff", "--no-color",
		"--no-renames", "--relative", "--no-prefix", ref, "--")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff -U0 %s: %w: %s", ref, err, strings.TrimSpace(stderr.String()))
	}
	lines, err := parseZeroContextDiff(string(out))
	if err != nil {
		return nil, err
	}
	ls := exec.CommandContext(context.Background(), "git", "ls-files", "-z", "--others", "--exclude-standard")
	ls.Dir = root
	names, err := ls.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %w", err)
	}
	for p := range strings.SplitSeq(strings.TrimSuffix(string(names), "\x00"), "\x00") {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		n := bytes.Count(data, []byte("\n"))
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
		all := make([]int, n)
		for i := range all {
			all[i] = i + 1
		}
		lines[p] = all
	}
	return lines, nil
}

func parseZeroContextDiff(diff string) (map[string][]int, error) {
	out := map[string][]int{}
	cur, header := "", false
	for l := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			cur, header = "", true
		case header && strings.HasPrefix(l, "+++ "):
			p, err := diffPath(strings.TrimPrefix(l, "+++ "))
			if err != nil {
				return nil, err
			}
			cur = p
		case strings.HasPrefix(l, "@@ "):
			header = false
			if cur == "" {
				continue
			}
			start, n, err := hunkAdded(l)
			if err != nil {
				return nil, err
			}
			for i := range n {
				out[cur] = append(out[cur], start+i)
			}
		}
	}
	return out, nil
}

func diffPath(s string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if s == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("git diff: cannot unquote path %s: %w", s, err)
		}
		return u, nil
	}
	return s, nil
}

func hunkAdded(l string) (start, n int, err error) {
	fields := strings.Fields(l)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, fmt.Errorf("git diff: malformed hunk header %q", l)
	}
	s, c, hasCount := strings.Cut(fields[2][1:], ",")
	start, err = strconv.Atoi(s)
	n = 1
	if err == nil && hasCount {
		n, err = strconv.Atoi(c)
	}
	if err != nil || start < 0 || n < 0 {
		return 0, 0, fmt.Errorf("git diff: malformed hunk header %q", l)
	}
	return start, n, nil
}

func exists(root, p string) (bool, error) {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
