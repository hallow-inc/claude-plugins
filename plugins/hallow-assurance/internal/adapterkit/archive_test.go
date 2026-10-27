package adapterkit

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type tarEntry struct {
	name string
	typ  byte
	mode int64
	link string
	body []byte
}

func buildTar(t fataler, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: mode, Linkname: e.link, Size: int64(len(e.body)), Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatalf("tar header %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatalf("tar body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

// sandbox returns dst nested three directories below a fresh root, so a "../" escape of up
// to four levels lands in an existing directory inside root, where outsideDst can see it.
func sandbox(t fataler, base string) (root, dst string) {
	t.Helper()
	root, err := os.MkdirTemp(base, "sb")
	if err != nil {
		t.Fatalf("%v", err)
	}
	dst = filepath.Join(root, "p1", "p2", "p3", "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	return root, dst
}

func outsideDst(t fataler, root, dst string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dst {
			return filepath.SkipDir
		}
		if d.IsDir() && strings.HasPrefix(dst, path+string(filepath.Separator)) {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// climbsOut is the lexical model of path resolution: it walks the segments from a starting
// depth and reports whether any ".." rises above depth zero.
func climbsOut(depth int, slashPath string) bool {
	for seg := range strings.SplitSeq(slashPath, "/") {
		switch seg {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		default:
			depth++
		}
	}
	return false
}

func pathSegments(alphabet ...string) *rapid.Generator[string] {
	return rapid.Custom(func(rt *rapid.T) string {
		return strings.Join(rapid.SliceOfN(rapid.SampledFrom(alphabet), 1, 4).Draw(rt, "segs"), "/")
	})
}

func archiveOf(t *testing.T, repo string) []byte {
	t.Helper()
	git(t, repo, "init", "-q")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "base")
	data, err := GitBytes(repo, "archive", "--format=tar", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExtractTarKeepsGitArchiveExecutableRunnable(t *testing.T) {
	isolateGitConfig(t)
	repo := realTempDir(t)
	writeFile(t, filepath.Join(repo, "tools", "run.sh"), "#!/bin/sh\necho base-tool\n")
	if err := os.Chmod(filepath.Join(repo, "tools", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "plain.txt"), "data\n")
	data := archiveOf(t, repo)

	dst := realTempDir(t)
	if err := ExtractTar(data, dst); err != nil {
		t.Fatalf("ExtractTar(git archive) = %v", err)
	}
	out, err := exec.CommandContext(t.Context(), filepath.Join(dst, "tools", "run.sh")).CombinedOutput()
	if err != nil || string(out) != "base-tool\n" {
		t.Fatalf("running the extracted 0755 script = %q, %v; a base-tree tool that loses its exec bit fails with permission denied and makes a fix test red for the wrong reason", out, err)
	}
	info, err := os.Stat(filepath.Join(dst, "plain.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("plain.txt mode = %v, want no exec bits; git recorded it as 0644", info.Mode())
	}
}

func TestExtractTarKeepsOwnerPermissionBits(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		mode := rapid.SampledFrom([]int64{0o600, 0o644, 0o700, 0o755}).Draw(rt, "mode")
		_, dst := sandbox(rt, base)
		if err := ExtractTar(buildTar(rt, tarEntry{name: "d/f", typ: tar.TypeReg, mode: mode, body: []byte("x")}), dst); err != nil {
			rt.Fatalf("ExtractTar = %v", err)
		}
		info, err := os.Stat(filepath.Join(dst, "d", "f"))
		if err != nil {
			rt.Fatalf("%v", err)
		}
		if got := int64(info.Mode().Perm() & 0o700); got != mode&0o700 {
			rt.Fatalf("owner bits = %o, want %o from tar mode %o; the base tree must run with the permissions it was committed with", got, mode&0o700, mode)
		}
	})
}

func TestExtractTarRecreatesGitArchiveSymlink(t *testing.T) {
	isolateGitConfig(t)
	repo := realTempDir(t)
	writeFile(t, filepath.Join(repo, "data", "fixture.txt"), "fixture body\n")
	links := map[string]string{
		"top-link":       "data/fixture.txt",
		"sub/nested-ln":  "../data/fixture.txt",
		"sub/dir-link":   "../data",
		"sub/inner/up-2": "../../data/fixture.txt",
	}
	for name, target := range links {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	data := archiveOf(t, repo)

	dst := realTempDir(t)
	if err := ExtractTar(data, dst); err != nil {
		t.Fatalf("ExtractTar(git archive with in-tree symlinks) = %v; links that stay inside the tree must be accepted", err)
	}
	for name, target := range links {
		got, err := os.Readlink(filepath.Join(dst, name))
		if err != nil || got != target {
			t.Fatalf("Readlink(%s) = %q, %v; want %q, a test reading a symlinked fixture on base would otherwise see a missing file", name, got, err, target)
		}
	}
	body, err := os.ReadFile(filepath.Join(dst, "sub", "dir-link", "fixture.txt"))
	if err != nil || string(body) != "fixture body\n" {
		t.Fatalf("reading through sub/dir-link = %q, %v; want the fixture", body, err)
	}
}

func TestExtractTarRejectsExactlyTheSymlinksThatEscapeDst(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		root, dst := sandbox(rt, base)
		dirs := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b"}), 0, 2).Draw(rt, "dirs")
		name := strings.Join(append(slices.Clone(dirs), "l"), "/")
		rel := pathSegments("..", ".", "a", "b").Draw(rt, "target")
		absolute := rapid.Bool().Draw(rt, "absolute")
		escapes := absolute || climbsOut(len(dirs), rel)
		targetIn := func(root string) string {
			if absolute {
				return filepath.Join(root, rel)
			}
			return rel
		}
		target := targetIn(root)

		err := ExtractTar(buildTar(rt, tarEntry{name: name, typ: tar.TypeSymlink, link: target}), dst)
		got, rlErr := os.Readlink(filepath.Join(dst, filepath.FromSlash(name)))
		switch {
		case escapes && err == nil:
			rt.Fatalf("symlink %s -> %s was accepted; it resolves outside dst, and a later file entry under it would write outside the base tree", name, target)
		case escapes && rlErr == nil:
			rt.Fatalf("rejected symlink %s -> %s was still created", name, target)
		case !escapes && (err != nil || got != target):
			rt.Fatalf("in-tree symlink %s -> %s: err=%v, readlink=%q, %v; links inside the tree must be recreated with their target", name, target, err, got, rlErr)
		}

		root2, dst2 := sandbox(rt, base)
		_ = ExtractTar(buildTar(rt,
			tarEntry{name: name, typ: tar.TypeSymlink, link: targetIn(root2)},
			tarEntry{name: name + "/pwn", typ: tar.TypeReg, body: []byte("pwned")},
		), dst2)
		if found := outsideDst(rt, root2, dst2); len(found) > 0 {
			rt.Fatalf("link %s -> %s then file %s/pwn wrote outside dst: %q", name, targetIn(root2), name, found)
		}
	})
}

func TestExtractTarRejectsLinkToExistingOutsideDirThenFileUnderIt(t *testing.T) {
	cases := map[string]func(root string) tarEntry{
		"relative": func(string) tarEntry {
			return tarEntry{name: "evil", typ: tar.TypeSymlink, link: "../outside"}
		},
		"subdir climbing out": func(string) tarEntry {
			return tarEntry{name: "a/evil", typ: tar.TypeSymlink, link: "../../outside"}
		},
		"absolute": func(root string) tarEntry {
			return tarEntry{name: "evil", typ: tar.TypeSymlink, link: filepath.Join(root, "p1", "p2", "p3", "outside")}
		},
	}
	for label, mk := range cases {
		t.Run(label, func(t *testing.T) {
			root, dst := sandbox(t, t.TempDir())
			outside := filepath.Join(root, "p1", "p2", "p3", "outside")
			if err := os.Mkdir(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			link := mk(root)
			err := ExtractTar(buildTar(t, link, tarEntry{name: link.name + "/pwn", typ: tar.TypeReg, body: []byte("pwned")}), dst)
			if err == nil {
				t.Fatalf("ExtractTar accepted %s -> %s followed by %s/pwn", link.name, link.link, link.name)
			}
			entries, rdErr := os.ReadDir(outside)
			if rdErr != nil || len(entries) != 0 {
				t.Fatalf("outside dir after extraction = %v, %v; want it untouched, the archive must never write outside dst", entries, rdErr)
			}
		})
	}
}

func TestExtractTarRejectsUnsafeEntryNames(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		root, dst := sandbox(rt, base)
		name := pathSegments("..", ".", "a").Draw(rt, "dirs") + "/f"
		unsafe := climbsOut(0, name)
		if rapid.Bool().Draw(rt, "absolute") {
			name, unsafe = filepath.Join(root, "p1", "p2", "p3", name), true
		}
		typ := rapid.SampledFrom([]byte{tar.TypeReg, tar.TypeDir, tar.TypeSymlink}).Draw(rt, "type")

		entry := tarEntry{name: name, typ: typ, link: "."}
		if typ == tar.TypeReg {
			entry.body = []byte("pwned")
		}
		err := ExtractTar(buildTar(rt, entry), dst)
		if unsafe && err == nil {
			rt.Fatalf("entry %q (type %c) was accepted; it names a path outside dst", name, typ)
		}
		if !unsafe && err != nil {
			rt.Fatalf("entry %q (type %c) inside the tree was rejected: %v", name, typ, err)
		}
		if found := outsideDst(rt, root, dst); len(found) > 0 {
			rt.Fatalf("entry %q (type %c) wrote outside dst: %q", name, typ, found)
		}
	})
}

func TestExtractTarRoundTripsFileContentAndNames(t *testing.T) {
	base := t.TempDir()
	runes := []rune{'a', 'z', '-', ' ', '"', '\\', '\t', '\n', 'é', '日'}
	seg := rapid.StringOfN(rapid.SampledFrom(runes), 1, 6, -1)
	path := rapid.Custom(func(rt *rapid.T) string {
		dirs := rapid.SliceOfN(seg, 0, 2).Draw(rt, "dirs")
		parts := make([]string, 0, len(dirs)+1)
		for _, d := range dirs {
			parts = append(parts, "d"+d)
		}
		return strings.Join(append(parts, "f"+seg.Draw(rt, "leaf")), "/")
	})
	rapid.Check(t, func(rt *rapid.T) {
		_, dst := sandbox(rt, base)
		names := rapid.SliceOfNDistinct(path, 1, 6, strings.ToLower).Draw(rt, "names")
		want := map[string][]byte{}
		entries := make([]tarEntry, 0, len(names))
		for _, n := range names {
			body := rapid.SliceOf(rapid.Byte()).Draw(rt, "body")
			want[filepath.FromSlash(n)] = body
			entries = append(entries, tarEntry{name: n, typ: tar.TypeReg, body: body})
		}
		if err := ExtractTar(buildTar(rt, entries...), dst); err != nil {
			rt.Fatalf("ExtractTar = %v", err)
		}
		got := map[string][]byte{}
		err := filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dst, p)
			got[rel], err = os.ReadFile(p)
			return err
		})
		if err != nil {
			rt.Fatalf("%v", err)
		}
		if len(got) != len(want) {
			rt.Fatalf("extracted %d files, want %d: got names %q", len(got), len(want), slices.Collect(maps.Keys(got)))
		}
		for n, body := range want {
			if !bytes.Equal(got[n], body) {
				rt.Fatalf("%q = %x, want %x; the base tree must hold exactly the committed bytes", n, got[n], body)
			}
		}
	})
}

func TestExtractTarReportsTruncatedArchive(t *testing.T) {
	data := buildTar(t, tarEntry{name: "f", typ: tar.TypeReg, body: []byte("hello")})
	for _, n := range []int{300, 515} {
		if err := ExtractTar(data[:n], t.TempDir()); err == nil {
			t.Fatalf("ExtractTar on an archive cut at byte %d returned nil; a half-materialized base tree must not look complete", n)
		}
	}
}

func declaresOver(data []byte, limit int64) bool {
	tr := tar.NewReader(bytes.NewReader(data))
	var total int64
	for {
		h, err := tr.Next()
		if err != nil {
			return false
		}
		total += h.Size
		if h.Size < 0 || total > limit {
			return true
		}
	}
}

func FuzzExtractTarStaysInsideDst(f *testing.F) {
	f.Add(buildTar(f, tarEntry{name: "evil", typ: tar.TypeSymlink, link: "../.."}, tarEntry{name: "evil/pwn", typ: tar.TypeReg, body: []byte("x")}))
	f.Add(buildTar(f, tarEntry{name: "a/l", typ: tar.TypeSymlink, link: "../../x"}, tarEntry{name: "a/l/pwn", typ: tar.TypeReg}))
	f.Add(buildTar(f, tarEntry{name: "../x", typ: tar.TypeReg, body: []byte("x")}))
	f.Add(buildTar(f, tarEntry{name: "d", typ: tar.TypeDir}, tarEntry{name: "d/l", typ: tar.TypeSymlink, link: ".."}, tarEntry{name: "d/l/f", typ: tar.TypeReg, mode: 0o755, body: []byte("ok")}))
	f.Fuzz(func(t *testing.T, data []byte) {
		if declaresOver(data, 1<<16) {
			t.Skip("sparse headers expand a few bytes into gigabytes of zeros; containment does not depend on size")
		}
		root, dst := sandbox(t, t.TempDir())
		_ = ExtractTar(data, dst)
		if found := outsideDst(t, root, dst); len(found) > 0 {
			t.Fatalf("archive wrote outside dst: %q", found)
		}
	})
}

func TestExtractTarRejectsEscapeThroughAnEarlierInTreeSymlink(t *testing.T) {
	root, dst := sandbox(t, t.TempDir())
	err := ExtractTar(buildTar(t,
		tarEntry{name: "d", typ: tar.TypeSymlink, link: "."},
		tarEntry{name: "d/e", typ: tar.TypeSymlink, link: ".."},
		tarEntry{name: "e/pwn", typ: tar.TypeReg, body: []byte("pwned")},
	), dst)
	if found := outsideDst(t, root, dst); len(found) > 0 {
		t.Fatalf("ExtractTar = %v and wrote outside dst: %q; d -> . makes d/e land at dst/e, so the lexical check on d/e -> .. passes while the link points at dst's parent", err, found)
	}
	if err == nil {
		t.Fatal("ExtractTar accepted d -> ., d/e -> .., e/pwn; the second link resolves outside dst")
	}
}
