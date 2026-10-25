package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func gitBytes(dir string, args ...string) ([]byte, error) {
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
			if err := writeFrom(target, tr, h.Size, h.FileInfo().Mode().Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func writeFrom(target string, r io.Reader, size int64, perm os.FileMode) error {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
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
		if !ok || len(fields) != 3 || fields[1] != "blob" {
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
		switch fields[0] {
		case "120000":
			err = os.Symlink(string(data), target)
		case "100755":
			err = os.WriteFile(target, data, 0o755)
		default:
			err = os.WriteFile(target, data, 0o644)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func ancestors(top, unit string) []string {
	var out []string
	for dir := unit; dir != top && filepath.Dir(dir) != dir; {
		dir = filepath.Dir(dir)
		out = append(out, dir)
	}
	return out
}

// baseTree mirrors the git top-level under dst at ref: the unit's tree and each ancestor
// directory's own files (a unit's tests may rely on a parent conftest.py or pyproject.toml).
func baseTree(top, ref, unit, dst string) error {
	if err := extractTree(top, ref, relSlash(top, unit), filepath.Join(dst, relSlash(top, unit))); err != nil {
		return err
	}
	for _, dir := range ancestors(top, unit) {
		if err := ancestorFiles(top, ref, relSlash(top, dir), dst); err != nil {
			return err
		}
	}
	return nil
}

func copyEntry(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(link, dst)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// workTree mirrors the git top-level under dst with the current content of the unit's tracked and
// untracked-not-ignored files and each ancestor directory's own such files.
func workTree(top, unit, dst string) error {
	listed, err := gitPaths(unit, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	for _, l := range listed {
		src := filepath.Join(unit, filepath.FromSlash(l))
		if !exists(src) {
			continue
		}
		if err := copyEntry(src, filepath.Join(dst, relSlash(top, unit), filepath.FromSlash(l))); err != nil {
			return err
		}
	}
	for _, dir := range ancestors(top, unit) {
		listed, err := gitPaths(dir, "ls-files", "--cached", "--others", "--exclude-standard", "--", ".")
		if err != nil {
			return err
		}
		for _, l := range listed {
			src := filepath.Join(dir, filepath.FromSlash(l))
			if strings.Contains(l, "/") || !exists(src) {
				continue
			}
			if err := copyEntry(src, filepath.Join(dst, relSlash(top, dir), l)); err != nil {
				return err
			}
		}
	}
	return nil
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

func importRoots(unit string) []string {
	roots := []string{unit}
	if src := filepath.Join(unit, "src"); exists(src) {
		roots = append(roots, src)
	}
	return roots
}

// treeEnv puts the tree's unit (and its src/) ahead of the virtualenv's editable-install paths:
// sys.path entries from PYTHONPATH precede site-packages and the .pth files in it.
func treeEnv(treeUnit string) []string {
	path := strings.Join(importRoots(treeUnit), string(os.PathListSeparator))
	if old := os.Getenv("PYTHONPATH"); old != "" {
		path += string(os.PathListSeparator) + old
	}
	return []string{"PYTHONPATH=" + path}
}

var identifier = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_]*$`)

func topLevelPackages(treeUnit string) ([]string, error) {
	var out []string
	for _, root := range importRoots(treeUnit) {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() && identifier.MatchString(e.Name()) && exists(filepath.Join(root, e.Name(), "__init__.py")) {
				out = append(out, e.Name())
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

const probeScript = `import importlib.util, json, sys
out = {}
for name in sys.argv[1:]:
    spec = importlib.util.find_spec(name)
    out[name] = [] if spec is None else [spec.origin or ""] + list(spec.submodule_search_locations or [])
json.dump(out, sys.stdout)
`

// probeTree fails closed when any of the tree's top-level packages would import from outside it, as
// an editable install through a meta-path finder (rather than a .pth path) would make it.
func probeTree(python, top, treeRoot, treeUnit string) (string, error) {
	names, err := topLevelPackages(treeUnit)
	if err != nil || len(names) == 0 {
		return "", err
	}
	stdout, stderr, code, err := runToolEnv(treeUnit, treeEnv(treeUnit), python, append([]string{"-c", probeScript}, names...)...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s import probe exited %d: %s", python, code, capText(stderr))
	}
	var found map[string][]string
	if err := json.Unmarshal(stdout, &found); err != nil {
		return "", fmt.Errorf("%s import probe: %w", python, err)
	}
	inside := realPath(treeRoot) + string(filepath.Separator)
	var problems []string
	for _, n := range names {
		for _, l := range found[n] {
			if l != "" && !strings.HasPrefix(realPath(l), inside) {
				problems = append(problems, fmt.Sprintf("package %s imports from %s, not from the tree under test; the virtualenv's install of the project shadows it, so tests would run against the wrong code", n, l))
				break
			}
		}
	}
	return strings.Join(problems, "\n"), nil
}
