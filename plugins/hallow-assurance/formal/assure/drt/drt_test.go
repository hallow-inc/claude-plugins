package drt

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"pgregory.net/rapid"
)

const buildCommand = "cd formal/assure && mise exec -- lake build"

var decideModel = filepath.Join("..", ".lake", "build", "bin", "decide-model")

func requireModel(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs(decideModel)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(bin)
	switch {
	case errors.Is(err, fs.ErrNotExist) && os.Getenv("ASSURE_DRT") != "":
		t.Fatalf("decide-model is not built at %s and ASSURE_DRT is set; build it with: %s", bin, buildCommand)
	case errors.Is(err, fs.ErrNotExist):
		t.Skipf("decide-model is not built at %s; build it with: %s, and set ASSURE_DRT=1 to require it", bin, buildCommand)
	case err != nil:
		t.Fatalf("decide-model at %s: %v", bin, err)
	}
	return bin
}

type decided struct {
	line string
	goes core.Status
}

func runModel(t *testing.T, bin string, cases []decided) []string {
	t.Helper()
	var in bytes.Buffer
	for _, c := range cases {
		in.WriteString(c.line)
		in.WriteByte('\n')
	}
	cmd := exec.Command(bin)
	cmd.Stdin = &in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "error:") && i < len(cases) {
			t.Fatalf("decide-model rejected case %d (generator left the model's domain): %s\ncase: %s", i, l, cases[i].line)
		}
	}
	if err != nil {
		t.Fatalf("decide-model: %v\nstderr: %s", err, stderr.String())
	}
	if len(lines) != len(cases) {
		t.Fatalf("decide-model wrote %d lines for %d cases", len(lines), len(cases))
	}
	return lines
}

func TestModelAgreesWithDecideObjective(t *testing.T) {
	bin := requireModel(t)
	var cases []decided
	rapid.Check(t, func(t *rapid.T) {
		c := genCase().Draw(t, "case")
		line, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		st, err := c.decideGo()
		if err != nil {
			t.Fatalf("waivers rejected by core.ParseWaivers: %v\ncase: %s", err, line)
		}
		cases = append(cases, decided{line: string(line), goes: st})
	})
	if t.Failed() {
		return
	}
	seen := map[string]int{}
	for i, model := range runModel(t, bin, cases) {
		seen[model]++
		if model != string(cases[i].goes) {
			t.Fatalf("case %d of %d: Go DecideObjective = %s, decide-model = %s\ncase: %s", i, len(cases), cases[i].goes, model, cases[i].line)
		}
	}
	t.Logf("%d cases agree; statuses %v", len(cases), seen)
}
