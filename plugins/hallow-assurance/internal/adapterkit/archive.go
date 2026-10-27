package adapterkit

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ExtractTar(data []byte, dst string) (err error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractEntry(root, tr, h); err != nil {
			return err
		}
	}
}

func extractEntry(root *os.Root, tr *tar.Reader, h *tar.Header) error {
	name := filepath.Clean(filepath.FromSlash(h.Name))
	if !filepath.IsLocal(name) {
		return fmt.Errorf("git archive: unsafe path %q", h.Name)
	}
	switch h.Typeflag {
	case tar.TypeDir:
		return root.MkdirAll(name, 0o755)
	case tar.TypeReg:
		if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		return writeFrom(root, name, tr, h.Size, h.FileInfo().Mode().Perm())
	case tar.TypeSymlink:
		if filepath.IsAbs(h.Linkname) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), filepath.FromSlash(h.Linkname))) {
			return fmt.Errorf("git archive: symlink %q points outside the tree: %q", h.Name, h.Linkname)
		}
		if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		return root.Symlink(h.Linkname, name)
	}
	return nil
}

func writeFrom(root *os.Root, name string, r io.Reader, size int64, perm os.FileMode) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, r, size)
	return errors.Join(err, f.Close())
}
