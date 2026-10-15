package qualification

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var (
	gated  bool
	binDir string
)

var caseTools = map[string][]string{
	"go": {"go", "gofmt", "git", "golangci-lint", "gremlins"},
}

func languages() []string {
	return slices.Sorted(maps.Keys(caseTools))
}

func adapterName(lang string) string {
	return "assure-adapter-" + lang
}

func TestMain(m *testing.M) {
	if real := os.Getenv("QUAL_REAL_ADAPTER"); real != "" {
		os.Exit(wrapAdapter(real, os.Getenv("QUAL_EMPTY_OBJECTIVE"), os.Getenv("QUAL_EMPTY_LOG"), os.Args[1:]))
	}
	flag.Parse()
	gated = os.Getenv("ASSURE_QUALIFICATION") == "1" && !testing.Short()
	if !gated {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "assure-qual-bin")
	if err != nil {
		panic(err)
	}
	pkgs := map[string]string{"assure": "../cmd/assure"}
	for _, lang := range languages() {
		pkgs[adapterName(lang)] = "../adapters/" + lang + "/" + adapterName(lang)
	}
	for name, pkg := range pkgs {
		build := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(dir, name), pkg)
		if out, err := build.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			panic(fmt.Sprintf("building %s: %v\n%s", name, err, out))
		}
	}
	binDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestCases(t *testing.T) {
	for _, lang := range languages() {
		t.Run(lang, func(t *testing.T) {
			files, err := filepath.Glob(filepath.Join(lang, "*.txtar"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) == 0 {
				t.Fatalf("no qualification cases under %s/", lang)
			}
			for _, f := range files {
				name := strings.TrimSuffix(filepath.Base(f), ".txtar")
				t.Run(name, func(t *testing.T) {
					data, err := os.ReadFile(f)
					if err != nil {
						t.Fatal(err)
					}
					c, err := parseCase(lang, name, data)
					if err != nil {
						t.Fatal(err)
					}
					if !gated {
						t.Skip("set ASSURE_QUALIFICATION=1 without -short to run assure evaluate")
					}
					runCase(t, c)
				})
			}
		})
	}
}

func TestBinHoldsAssureAndEveryAdapter(t *testing.T) {
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to build the binaries")
	}
	entries, err := os.ReadDir(binDir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := []string{"assure"}
	for _, lang := range languages() {
		want = append(want, adapterName(lang))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("bin dir holds %q, want %q", got, want)
	}
}

const (
	sideTable   = "the language table"
	sideAdapter = "an adapters/<l>/assure-adapter-<l> directory"
	sideCases   = "a qualification/<l>/ directory holding a .txtar case"
)

func languageGaps(table map[string][]string, adaptersRoot, casesRoot string) (map[string][]string, error) {
	present := map[string]map[string]bool{sideTable: {}, sideAdapter: {}, sideCases: {}}
	for lang := range table {
		present[sideTable][lang] = true
	}
	adapters, err := os.ReadDir(adaptersRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range adapters {
		if st, err := os.Stat(filepath.Join(adaptersRoot, e.Name(), adapterName(e.Name()))); err == nil && st.IsDir() {
			present[sideAdapter][e.Name()] = true
		}
	}
	dirs, err := os.ReadDir(casesRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range dirs {
		if !e.IsDir() {
			continue
		}
		files, err := filepath.Glob(filepath.Join(casesRoot, e.Name(), "*.txtar"))
		if err != nil {
			return nil, err
		}
		if len(files) > 0 {
			present[sideCases][e.Name()] = true
		}
	}
	all := map[string]bool{}
	for _, langs := range present {
		maps.Copy(all, langs)
	}
	gaps := map[string][]string{}
	for lang := range all {
		for _, side := range []string{sideTable, sideAdapter, sideCases} {
			if !present[side][lang] {
				gaps[lang] = append(gaps[lang], side)
			}
		}
	}
	return gaps, nil
}

func TestLanguagesCorrespond(t *testing.T) {
	gaps, err := languageGaps(caseTools, filepath.Join("..", "adapters"), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range slices.Sorted(maps.Keys(gaps)) {
		t.Errorf("language %s lacks %s", lang, strings.Join(gaps[lang], " and "))
	}
}

func TestLanguageGapsMatchLayout(t *testing.T) {
	scratch := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		root, err := os.MkdirTemp(scratch, "layout")
		if err != nil {
			rt.Fatal(err)
		}
		adaptersRoot, casesRoot := filepath.Join(root, "adapters"), filepath.Join(root, "qualification")
		writeLayout(rt, layout{dirs: []string{adaptersRoot, casesRoot}})
		table := map[string][]string{}
		want := map[string][]string{}
		for _, lang := range rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z]{1,3}`), 0, 4, func(l string) string { return l }).Draw(rt, "languages") {
			present := genLanguageLayout(rt, lang, adaptersRoot, casesRoot)
			if present[sideTable] {
				table[lang] = []string{"tool"}
			}
			var missing []string
			for _, side := range []string{sideTable, sideAdapter, sideCases} {
				if !present[side] {
					missing = append(missing, side)
				}
			}
			if len(missing) > 0 && len(missing) < 3 {
				want[lang] = missing
			}
		}
		got, err := languageGaps(table, adaptersRoot, casesRoot)
		if err != nil {
			rt.Fatal(err)
		}
		if !maps.EqualFunc(got, want, slices.Equal) {
			rt.Fatalf("gaps %q, want %q", got, want)
		}
	})
}

type layout struct{ dirs, files []string }

func genLanguageLayout(rt *rapid.T, lang, adaptersRoot, casesRoot string) map[string]bool {
	adapterDir := filepath.Join(adaptersRoot, lang, adapterName(lang))
	adapters := []layout{
		{},
		{dirs: []string{filepath.Dir(adapterDir)}},
		{dirs: []string{adapterDir + "x"}},
		{files: []string{adapterDir}},
		{dirs: []string{adapterDir}},
	}
	caseDir := filepath.Join(casesRoot, lang)
	caseName := rapid.StringMatching(`[a-z-]{1,8}`).Draw(rt, "case "+lang)
	cases := []layout{
		{},
		{dirs: []string{caseDir}},
		{files: []string{caseDir + ".txtar", filepath.Join(caseDir, caseName+".txt")}},
		{files: []string{filepath.Join(caseDir, "sub", caseName+".txtar")}},
		{files: []string{filepath.Join(caseDir, caseName+".txtar")}},
	}
	a := rapid.IntRange(0, len(adapters)-1).Draw(rt, "adapter "+lang)
	c := rapid.IntRange(0, len(cases)-1).Draw(rt, "cases "+lang)
	writeLayout(rt, adapters[a])
	writeLayout(rt, cases[c])
	return map[string]bool{
		sideTable:   rapid.Bool().Draw(rt, "table "+lang),
		sideAdapter: a == len(adapters)-1,
		sideCases:   c == len(cases)-1,
	}
}

func writeLayout(rt *rapid.T, l layout) {
	for _, d := range l.dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			rt.Fatal(err)
		}
	}
	for _, p := range l.files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			rt.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			rt.Fatal(err)
		}
	}
}

func runCase(t *testing.T, c qcase) {
	t.Helper()
	ms, log := evaluateCase(t, c, filepath.Join(binDir, adapterName(c.lang)), nil)
	for _, m := range ms {
		t.Error(m)
	}
	if len(ms) > 0 {
		t.Log(log)
	}
}

func evaluateCase(t *testing.T, c qcase, adapter string, env []string) ([]string, string) {
	t.Helper()
	root := realTempDir(t)
	base, err := buildRepo(t.Context(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	allowed := slices.DeleteFunc(slices.Clone(caseTools[c.lang]), func(n string) bool { return slices.Contains(c.omit, n) })
	tools, err := hostTools(allowed)
	if err != nil {
		t.Fatal(err)
	}
	tools["assure"] = filepath.Join(binDir, "assure")
	tools[adapterName(c.lang)] = adapter
	path, err := toolDir(t.TempDir(), tools)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.CommandContext(t.Context(), tools["assure"], "evaluate", "--changed-from", base, "--report", report, "--date", "2026-01-01")
	cmd.Dir = root
	cmd.Env = append(append(os.Environ(), "PATH="+path, "RAPID_SEED=1"), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		t.Fatalf("assure evaluate: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("reading report: %v\n%s", err, out.String())
	}
	rep, err := parseReport(data)
	if err != nil {
		t.Fatal(err)
	}
	return match(c.expect, rep), fmt.Sprintf("assure evaluate output:\n%s\nreport:\n%s", out.String(), data)
}

func TestCaseFilesAreNotHostFiles(t *testing.T) {
	if !gated {
		t.Skip("set ASSURE_QUALIFICATION=1 without -short to classify with the built assure")
	}
	var paths []string
	for _, lang := range languages() {
		n := len(paths)
		err := filepath.WalkDir(lang, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				paths = append(paths, filepath.ToSlash(filepath.Join("qualification", p)))
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == n {
			t.Fatalf("no files under %s/", lang)
		}
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(binDir, "assure"), append([]string{"classify"}, paths...)...)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("assure classify: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(paths) {
		t.Fatalf("classified %d of %d files:\n%s", len(lines), len(paths), out)
	}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) != 4 {
			t.Fatalf("unexpected classify line %q", l)
		}
		if f[2] != "-" || f[3] == "source" || f[3] == "test" {
			t.Errorf("%s is classified as language %s, role %s; seeded cases must stay invisible to the host gate", f[0], f[2], f[3])
		}
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
