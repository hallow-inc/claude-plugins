package adapterkit

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func ToolOutput(dir, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s not found on PATH", name)
	}
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}
