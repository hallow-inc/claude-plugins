package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type taskLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type listedTest struct {
	Name     string       `json:"name"`
	File     string       `json:"file"`
	Location taskLocation `json:"location"`
}

type testKey struct{ file, name string }

func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func vitestList(dir, bin, root string, files []string) ([]listedTest, error) {
	if len(files) == 0 {
		return nil, nil
	}
	tmp, err := os.MkdirTemp("", "assure-vitest-list-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	report := filepath.Join(tmp, "list.json")
	// "--json <path>" would take the first test file as the output path and overwrite it.
	stdout, stderr, code, err := runTool(dir, bin, append([]string{"list", "--json=" + report}, files...)...)
	if err != nil {
		return nil, err
	}
	data, rerr := os.ReadFile(report)
	if code != 0 || rerr != nil {
		return nil, fmt.Errorf("vitest list in %s exited %d: %s", dir, code, capText(append(stdout, stderr...)))
	}
	var out []listedTest
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("vitest list in %s: %w", dir, err)
	}
	for i := range out {
		out[i].File = relTo(root, out[i].File)
	}
	return out, nil
}

func changedTests(c runContext, unit string) ([]string, error) {
	paths, err := changedPaths(c.root, c.ref)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range paths {
		role, claimed, err := classify(p)
		if err != nil {
			return nil, err
		}
		if !claimed || role != "test" {
			continue
		}
		if u, ok := unitOf(c.top, filepath.Dir(filepath.Join(c.root, filepath.FromSlash(p)))); ok && u == unit {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func treeSpec(ref, rel string) string {
	if rel == "." {
		return ref + "^{tree}"
	}
	return ref + ":" + rel
}

func extractTree(top, ref, rel, dst string) error {
	id, err := gitBytes(top, "rev-parse", "--verify", treeSpec(ref, rel))
	if err != nil {
		return err
	}
	data, err := gitBytes(top, "archive", "--format=tar", strings.TrimSpace(string(id)))
	if err != nil {
		return err
	}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("git archive: unsafe path %q", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFrom(target, tr, h.Size); err != nil {
				return err
			}
		}
	}
}

func writeFrom(target string, r io.Reader, size int64) error {
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, r, size)
	return errors.Join(err, f.Close())
}

func ancestorFiles(top, ref, rel, dst string) error {
	listing, err := gitBytes(top, "ls-tree", "-z", treeSpec(ref, rel))
	if err != nil {
		return err
	}
	for entry := range strings.SplitSeq(string(listing), "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || fields[0] == "120000" {
			continue
		}
		data, err := gitBytes(top, "cat-file", "blob", fields[2])
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(rel), name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// baseTree mirrors the git top-level under dst: the unit's tree and each ancestor directory's own
// files at ref (a unit's tsconfig.json may extend one above it), and symlinks to the current
// node_modules directories at the same relative positions so hoisted packages resolve.
func baseTree(c runContext, unit, dst string) error {
	if err := extractTree(c.top, c.ref, relSlash(c.top, unit), filepath.Join(dst, relSlash(c.top, unit))); err != nil {
		return err
	}
	for dir := unit; ; dir = filepath.Dir(dir) {
		rel := relSlash(c.top, dir)
		if dir != unit {
			if err := ancestorFiles(c.top, c.ref, rel, dst); err != nil {
				return err
			}
		}
		if nm := filepath.Join(dir, "node_modules"); exists(nm) {
			link := filepath.Join(dst, filepath.FromSlash(rel), "node_modules")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(nm, link); err != nil {
				return err
			}
		}
		if dir == c.top || filepath.Dir(dir) == dir {
			return nil
		}
	}
}

func overlayTests(root, baseRoot string, tests []string) error {
	for _, p := range tests {
		target := filepath.Join(baseRoot, filepath.FromSlash(p))
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, src, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func existing(root string, files []string) []string {
	var out []string
	for _, f := range files {
		if p := filepath.Join(root, filepath.FromSlash(f)); exists(p) {
			out = append(out, p)
		}
	}
	return out
}

func newTests(head, base []listedTest) []listedTest {
	seen := map[testKey]bool{}
	for _, t := range base {
		seen[testKey{t.File, t.Name}] = true
	}
	var out []listedTest
	for _, t := range head {
		if !seen[testKey{t.File, t.Name}] {
			out = append(out, t)
		}
	}
	return out
}

func baseCase(t listedTest, f *vitestFile, output string) xmlCase {
	tc := xmlCase{Name: t.Name, Classname: t.File}
	var ran, skipped int
	failed := false
	var failures []string
	if f != nil {
		for _, a := range f.AssertionResults {
			if a.Location == nil || *a.Location != t.Location {
				continue
			}
			ran++
			if a.Status == "failed" {
				failed = true
				failures = append(failures, a.FailureMessages...)
			} else if slices.Contains(skippedStatuses, a.Status) {
				skipped++
			} else if a.Status != "passed" {
				tc.Error = &xmlText{Message: "unknown vitest status " + a.Status, Body: "no output"}
			}
		}
	}
	if ran == 0 {
		text := output
		if f != nil && strings.TrimSpace(f.Message) != "" {
			text = f.Message
		}
		return xmlCase{Name: t.Name, Classname: t.File, Error: &xmlText{Message: "did not run on the base commit", Body: capText([]byte(text))}}
	}
	if tc.Error != nil {
		return tc
	}
	if failed {
		tc.Failure = &xmlText{Message: "fails on the base commit", Body: capText([]byte(strings.Join(failures, "\n")))}
	} else if skipped == ran {
		tc.Skipped = &struct{}{}
	}
	return tc
}

func baseSuites(baseRoot string, selected []listedTest, report string, output []byte) []xmlSuite {
	files := map[string]*vitestFile{}
	var r vitestReport
	if data, err := os.ReadFile(report); err == nil && json.Unmarshal(data, &r) == nil {
		for i := range r.TestResults {
			files[relTo(baseRoot, r.TestResults[i].Name)] = &r.TestResults[i]
		}
	}
	byFile := map[string][]listedTest{}
	for _, t := range selected {
		byFile[t.File] = append(byFile[t.File], t)
	}
	var out []xmlSuite
	for _, name := range slices.Sorted(maps.Keys(byFile)) {
		s := xmlSuite{Name: name, Cases: []xmlCase{}}
		for _, t := range byFile[name] {
			s.Cases = append(s.Cases, baseCase(t, files[name], string(output)))
		}
		s.Tests = len(s.Cases)
		out = append(out, s)
	}
	return out
}

func unitFailOnBase(c runContext, unit, seed string) ([]xmlSuite, error) {
	tests, err := changedTests(c, unit)
	if err != nil {
		return nil, err
	}
	bin := c.tools[unit]["vitest"].bin
	head, err := vitestList(unit, bin, c.root, existing(c.root, tests))
	if err != nil || len(head) == 0 {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "assure-fail-on-base-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	dst := realPath(tmp)
	if err := baseTree(c, unit, dst); err != nil {
		return nil, err
	}
	baseRoot := filepath.Join(dst, filepath.FromSlash(relSlash(c.top, c.root)))
	baseUnit := filepath.Join(dst, filepath.FromSlash(relSlash(c.top, unit)))
	base, err := vitestList(baseUnit, bin, baseRoot, existing(baseRoot, tests))
	if err != nil {
		return nil, err
	}
	if err := overlayTests(c.root, baseRoot, tests); err != nil {
		return nil, err
	}
	selected := newTests(head, base)
	if len(selected) == 0 {
		return nil, nil
	}
	var files []string
	for _, t := range selected {
		files = append(files, t.File)
	}
	slices.Sort(files)
	report := filepath.Join(dst, "fail-on-base.json")
	args := append([]string{"run", "--includeTaskLocation", "--reporter=json", "--outputFile=" + report}, absFiles(baseRoot, slices.Compact(files))...)
	stdout, stderr, _, err := runToolEnv(baseUnit, []string{"ASSURE_SEED=" + seed}, bin, args...)
	if err != nil {
		return nil, err
	}
	return baseSuites(baseRoot, selected, report, append(stdout, stderr...)), nil
}

func runFailOnBase(c runContext) ([]evidence, string, error) {
	seed, err := assureSeed(c.root)
	if err != nil {
		return nil, "", err
	}
	suites := xmlSuites{Suites: []xmlSuite{}}
	for _, unit := range slices.Sorted(maps.Keys(c.sel)) {
		s, err := unitFailOnBase(c, unit, seed)
		if err != nil {
			return nil, "", err
		}
		suites.Suites = append(suites.Suites, s...)
	}
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(c.out, "fail-on-base.xml"), append([]byte(xml.Header), data...), 0o644); err != nil {
		return nil, "", err
	}
	return []evidence{{Type: "test.fail_on_base", Path: "fail-on-base.xml"}}, "", nil
}
