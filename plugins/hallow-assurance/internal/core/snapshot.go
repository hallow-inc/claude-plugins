package core

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	StateDir      = ".assure/state"
	ProvenanceDir = ".assure/provenance"
)

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidSession(id string) bool { return sessionID.MatchString(id) }

type SnapFile struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
}

type Snapshot struct {
	Version int        `json:"version"`
	Session string     `json:"session"`
	Files   []SnapFile `json:"files"`
}

func BlobHash(data []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func SnapshotPath(root, session string) string {
	return filepath.Join(root, StateDir, "snapshot-"+session+".json")
}

func (m *Manifest) protectedGlobs(extra []Glob) []Glob {
	gs := append(append([]Glob{}, m.Protected...), extra...)
	for _, s := range []string{ManifestName, ".assure/**"} {
		g, _ := CompileGlob(s)
		gs = append(gs, g)
	}
	for _, c := range m.Components {
		if c.Formal {
			g, _ := CompileGlob(path.Clean(c.Challenge))
			gs = append(gs, g)
		}
	}
	return gs
}

func walkRoot(g Glob) (root string, exact bool) {
	var lits []string
	for _, s := range g.segs {
		if !literal(s) {
			return path.Join(lits...), false
		}
		lits = append(lits, s)
	}
	return path.Join(lits...), true
}

func skipped(rel string) bool {
	return rel == ".git" || rel == StateDir || strings.HasPrefix(rel, StateDir+"/") ||
		rel == ProvenanceDir || strings.HasPrefix(rel, ProvenanceDir+"/")
}

func (m *Manifest) ProtectedFiles(extra []Glob) (map[string]string, error) {
	gs := m.protectedGlobs(extra)
	isProtected := func(rel string) bool {
		if m.IsProtected(rel) {
			return true
		}
		return anyMatch(extra, rel)
	}
	files := map[string]string{}
	for _, g := range gs {
		start, exact := walkRoot(g)
		err := filepath.WalkDir(filepath.Join(m.Root, filepath.FromSlash(start)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			rel, _ := m.Rel(p)
			if d.IsDir() {
				if skipped(rel) || exact {
					return filepath.SkipDir
				}
				return nil
			}
			if _, done := files[rel]; done || skipped(rel) || !isProtected(rel) {
				return nil
			}
			blob, err := blobOf(p, d)
			if err != nil {
				return err
			}
			files[rel] = blob
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func blobOf(p string, d fs.DirEntry) (string, error) {
	if d.Type()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		return BlobHash([]byte(target)), err
	}
	data, err := os.ReadFile(p)
	return BlobHash(data), err
}

func TakeSnapshot(m *Manifest, session string, extra []Glob) (Snapshot, error) {
	files, err := m.ProtectedFiles(extra)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Session: session, Files: []SnapFile{}}
	for p, b := range files {
		s.Files = append(s.Files, SnapFile{Path: p, Blob: b})
	}
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	return s, nil
}

func WriteSnapshot(m *Manifest, s Snapshot) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeState(SnapshotPath(m.Root, s.Session), data)
}

func writeState(file string, data []byte) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}

func ReadSnapshot(m *Manifest, session string) (Snapshot, error) {
	s, err := readSnapshot(m, session)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrSnapshotMissing, err)
	}
	return s, nil
}

func readSnapshot(m *Manifest, session string) (Snapshot, error) {
	data, err := os.ReadFile(SnapshotPath(m.Root, session))
	if err != nil {
		return Snapshot{}, err
	}
	vs, err := schemas.Validate(schemas.Snapshot, data)
	if err == nil && len(vs) > 0 {
		err = errors.New(vs[0].String())
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("invalid snapshot: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return Snapshot{}, err
	}
	if s.Session != session {
		return Snapshot{}, fmt.Errorf("snapshot file is for session %s, not %s", s.Session, session)
	}
	return s, nil
}

func Drift(m *Manifest, s Snapshot, extra []Glob) ([]string, error) {
	now, err := m.ProtectedFiles(extra)
	if err != nil {
		return nil, err
	}
	var out []string
	before := map[string]string{}
	for _, f := range s.Files {
		before[f.Path] = f.Blob
		switch b, ok := now[f.Path]; {
		case !ok:
			out = append(out, "removed "+f.Path)
		case b != f.Blob:
			out = append(out, "changed "+f.Path)
		}
	}
	for p := range now {
		if _, ok := before[p]; !ok {
			out = append(out, "added "+p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func PruneState(root string, maxAge time.Duration, now time.Time) {
	for _, pattern := range []string{"snapshot-*.json", "stop-*.json", "subagent-stop-*.json", "inspections/*.sarif", "pending/*/*.json"} {
		matches, _ := filepath.Glob(filepath.Join(root, StateDir, pattern))
		for _, f := range matches {
			if info, err := os.Stat(f); err == nil && now.Sub(info.ModTime()) > maxAge {
				_ = os.Remove(f)
			}
		}
	}
}
