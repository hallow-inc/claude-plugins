package qualification

import (
	"bytes"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/adapterkit"
)

func testsPassCase(t *testing.T, lang, file, test string) (adapterkit.XMLCase, string, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lang, file))
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseCase(lang, strings.TrimSuffix(file, ".txtar"), data)
	if err != nil {
		t.Fatal(err)
	}
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to run the built adapter")
	}
	p := prepareCase(t, c, filepath.Join(binDir, adapterName(lang)))
	out := filepath.Join(t.TempDir(), "ev")
	cmd := exec.CommandContext(t.Context(), p.tools[adapterName(lang)], "run", "VER-TESTS-PASS", "--changed-from", p.base, "--out", out)
	cmd.Dir = p.root
	cmd.Env = append(os.Environ(), "PATH="+p.path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("adapter: %v\n%s; a failing or flaky test is evidence, and the adapter must exit 0", err, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var s adapterkit.XMLSuites
	if err := xml.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	var found []adapterkit.XMLCase
	for _, su := range s.Suites {
		found = append(found, slices.DeleteFunc(su.Cases, func(x adapterkit.XMLCase) bool { return x.Name != test })...)
	}
	if len(found) != 1 || found[0].Failure == nil || !strings.Contains(found[0].Failure.Body, "attempt 1:") {
		t.Fatalf("want one %s case holding its first run's failure\n%s", test, raw)
	}
	return found[0], p.root, raw
}

type rerunCase struct {
	file, test string
	flaky      []string
	ran        string
}

func checkReruns(t *testing.T, lang, countDir string, cases []rerunCase) {
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			c, root, raw := testsPassCase(t, lang, tc.file, tc.test)
			var flaky []string
			for _, pr := range c.Properties {
				if pr.Name == "assure.flaky" {
					flaky = append(flaky, pr.Value)
				}
			}
			if !slices.Equal(flaky, tc.flaky) {
				t.Fatalf("%s assure.flaky %q, want %q; a test that passes on a rerun is flaky, one that fails every round is a real failure, and the two must not be confused\n%s", tc.test, flaky, tc.flaky, raw)
			}
			ran, err := os.ReadFile(filepath.Join(root, countDir, tc.test+".count"))
			if err != nil {
				t.Fatal(err)
			}
			if string(ran) != tc.ran {
				t.Fatalf("%s ran %s times, want %s: the first run plus a rerun per round until it passes, at most 3", tc.test, ran, tc.ran)
			}
		})
	}
}

func TestGoReruns(t *testing.T) {
	checkReruns(t, "go", "p", []rerunCase{
		{"flaky.txtar", "TestDoubleFlaky", []string{"2"}, "3"},
		{"fails-every-round.txtar", "TestDoubleAlways", nil, "4"},
	})
}

func TestPythonReruns(t *testing.T) {
	checkReruns(t, "python", ".", []rerunCase{
		{"flaky.txtar", "test_double_flaky", []string{"2"}, "3"},
		{"fails-every-round.txtar", "test_double_always", nil, "4"},
	})
}

func TestTypeScriptReruns(t *testing.T) {
	checkReruns(t, "typescript", ".", []rerunCase{
		{"flaky.txtar", "double > of 2 (flaky+)", []string{"2"}, "3"},
		{"fails-every-round.txtar", "double > of 2 (always+)", nil, "4"},
	})
}
