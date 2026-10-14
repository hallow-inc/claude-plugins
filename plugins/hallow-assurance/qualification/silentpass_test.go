package qualification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func wrapAdapter(real, objective, logPath string, args []string) int {
	cmd := exec.CommandContext(context.Background(), real, args...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "QUAL_") })
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, os.Stderr
	err := cmd.Run()
	code := 0
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	} else if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "qualification wrapper: %v\n", err)
		return 1
	}
	if code == 0 && len(args) >= 2 && args[0] == "run" && args[1] == objective {
		if err := emptyEvidence(args[2:], out.Bytes(), logPath); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "qualification wrapper: %v\n", err)
			return 1
		}
	}
	if _, err := os.Stdout.Write(out.Bytes()); err != nil {
		return 1
	}
	return code
}

func emptyEvidence(flags []string, resp []byte, logPath string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("out", "", "")
	fs.String("changed-from", "", "")
	if err := fs.Parse(flags); err != nil || *out == "" {
		return fmt.Errorf("run flags %q name no --out directory", flags)
	}
	var r struct {
		Evidence []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("run response: %w", err)
	}
	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	for _, e := range r.Evidence {
		doc, ok := emptyDocs[e.Type]
		if !ok {
			return fmt.Errorf("no empty document for evidence kind %q", e.Type)
		}
		if err := os.WriteFile(filepath.Join(*out, filepath.FromSlash(e.Path)), doc, 0o644); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(log, e.Type); err != nil {
			return err
		}
	}
	return nil
}

func catalogEvidence(t *testing.T) map[string]string {
	t.Helper()
	cat, err := core.LoadCatalog("v0")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, o := range cat.Objectives {
		kinds[o.ID] = o.Evidence
	}
	return kinds
}

func checkEmptyDocsCoverAdapter(t *testing.T, kinds map[string]string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), filepath.Join(binDir, "assure-adapter-go"), "describe").Output()
	if err != nil {
		t.Fatalf("assure-adapter-go describe: %v", err)
	}
	var d struct {
		Objectives map[string]json.RawMessage `json:"objectives"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	for _, id := range slices.Sorted(maps.Keys(d.Objectives)) {
		if _, ok := emptyDocs[kinds[id]]; !ok {
			t.Errorf("the Go adapter implements %s, whose evidence kind %q has no empty document", id, kinds[id])
		}
	}
}

func TestSilentPass(t *testing.T) {
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to rerun seeded cases with emptied evidence")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	kinds := catalogEvidence(t)
	checkEmptyDocsCoverAdapter(t, kinds)
	cases := loadCases(t)
	for _, name := range slices.Sorted(maps.Keys(cases)) {
		obj := cases[name].seeds
		if obj == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("go", name+".txtar"))
			if err != nil {
				t.Fatal(err)
			}
			c, err := parseCase(name, data)
			if err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "emptied")
			ms, log := evaluateCase(t, c, self, []string{
				"QUAL_REAL_ADAPTER=" + filepath.Join(binDir, "assure-adapter-go"),
				"QUAL_EMPTY_OBJECTIVE=" + obj,
				"QUAL_EMPTY_LOG=" + logPath,
			})
			checkSilentPass(t, obj, kinds[obj], logPath, ms, log)
		})
	}
}

func checkSilentPass(t *testing.T, obj, kind, logPath string, ms []string, log string) {
	t.Helper()
	emptied, err := os.ReadFile(logPath)
	if err != nil || string(emptied) != kind+"\n" {
		t.Fatalf("the wrapper emptied %q (err %v), want exactly one %s document\n%s", emptied, err, kind, log)
	}
	own := entryKey{obj, "go"}.String() + ": "
	var flipped, other []string
	for _, m := range ms {
		if strings.HasPrefix(m, own) {
			flipped = append(flipped, m)
		} else {
			other = append(other, m)
		}
	}
	if len(flipped) == 0 {
		t.Errorf("silent pass: with its %s evidence emptied, %s still matches its expectation\n%s", kind, obj, log)
	}
	if len(other) > 0 {
		t.Errorf("emptying %s evidence changed verdicts it does not feed, so the run is not comparable: %q\n%s", obj, other, log)
	}
	for _, m := range flipped {
		t.Log(m)
	}
}
