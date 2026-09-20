// Package store owns the on-disk bundle layout. The bundle doubles as the
// user's permanent local mirror, so everything here is append-only and safe to
// merge with another bundle using plain shell tools.
package store

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ManifestName is the canonical, append-only record of every artifact.
const ManifestName = "MANIFEST.tsv"

// SumsName is a regenerable sha256sum(1) compatible file used by verify.sh.
const SumsName = "SHA256SUMS"

// Entry is one line of MANIFEST.tsv.
type Entry struct {
	Eco     string // apt | npm | pypi | docker
	SHA256  string
	Size    int64
	RelPath string // path relative to the bundle root, always slash separated
	Source  string // origin URL
	AddedAt string // RFC3339 UTC
}

func (e Entry) line() string {
	return strings.Join([]string{e.Eco, e.SHA256, strconv.FormatInt(e.Size, 10), e.RelPath, e.Source, e.AddedAt}, "\t")
}

func parseLine(s string) (Entry, bool) {
	f := strings.Split(s, "\t")
	if len(f) < 6 {
		return Entry{}, false
	}
	size, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return Entry{}, false
	}
	return Entry{Eco: f[0], SHA256: f[1], Size: size, RelPath: f[3], Source: f[4], AddedAt: f[5]}, true
}

// Store is the bundle root plus an optional delta root. Artifacts are always
// written to the bundle root; when a delta root is configured, newly added
// artifacts are additionally hard linked (or copied) there so the user can
// carry only the delta to the air-gapped side.
type Store struct {
	Root  string
	Delta string

	mu      sync.Mutex
	bySHA   map[string]Entry
	byPath  map[string]Entry
	added   []Entry
	logFile *os.File
}

// Open reads an existing bundle (or creates an empty one) at root.
func Open(root, delta string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if delta != "" {
		if err := os.MkdirAll(delta, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{Root: root, Delta: delta, bySHA: map[string]Entry{}, byPath: map[string]Entry{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, ManifestName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s.logFile = f
	return s, nil
}

func (s *Store) load() error {
	f, err := os.Open(filepath.Join(s.Root, ManifestName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e, ok := parseLine(line)
		if !ok {
			continue
		}
		s.bySHA[e.SHA256] = e
		s.byPath[e.RelPath] = e
	}
	return sc.Err()
}

// Close flushes the manifest handle.
func (s *Store) Close() error {
	if s.logFile == nil {
		return nil
	}
	return s.logFile.Close()
}

// Abs resolves a bundle relative path.
func (s *Store) Abs(rel string) string {
	return filepath.Join(s.Root, filepath.FromSlash(rel))
}

// HasSHA reports whether an artifact with this digest is already in the mirror
// and still present on disk.
func (s *Store) HasSHA(sha string) (Entry, bool) {
	s.mu.Lock()
	e, ok := s.bySHA[sha]
	s.mu.Unlock()
	if !ok {
		return Entry{}, false
	}
	if st, err := os.Stat(s.Abs(e.RelPath)); err != nil || st.Size() != e.Size {
		return Entry{}, false
	}
	return e, true
}

// HasPath reports whether this exact bundle path is already recorded and
// present on disk with the recorded size.
func (s *Store) HasPath(rel string) (Entry, bool) {
	s.mu.Lock()
	e, ok := s.byPath[rel]
	s.mu.Unlock()
	if !ok {
		return Entry{}, false
	}
	if st, err := os.Stat(s.Abs(rel)); err != nil || st.Size() != e.Size {
		return Entry{}, false
	}
	return e, true
}

// Record appends an entry to the manifest and mirrors the file into the delta
// root when one is configured. It is safe for concurrent use.
func (s *Store) Record(e Entry) error {
	if e.AddedAt == "" {
		e.AddedAt = time.Now().UTC().Format(time.RFC3339)
	}
	e.RelPath = filepath.ToSlash(e.RelPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintln(s.logFile, e.line()); err != nil {
		return err
	}
	s.bySHA[e.SHA256] = e
	s.byPath[e.RelPath] = e
	s.added = append(s.added, e)
	if s.Delta != "" {
		if err := linkOrCopy(s.Abs(e.RelPath), filepath.Join(s.Delta, filepath.FromSlash(e.RelPath))); err != nil {
			return err
		}
	}
	return nil
}

// Added returns the entries recorded during this run.
func (s *Store) Added() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.added))
	copy(out, s.added)
	return out
}

// All returns every entry currently known, sorted by path.
func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.byPath))
	for _, e := range s.byPath {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out
}

// AddedBytes is the total size of everything downloaded during this run.
func (s *Store) AddedBytes() int64 {
	var n int64
	for _, e := range s.Added() {
		n += e.Size
	}
	return n
}

// WriteSums regenerates SHA256SUMS for the whole mirror (and for the delta
// root, covering only the delta's own files).
func (s *Store) WriteSums() error {
	if err := writeSums(s.Root, s.All()); err != nil {
		return err
	}
	if s.Delta != "" {
		return writeSums(s.Delta, s.Added())
	}
	return nil
}

func writeSums(root string, entries []Entry) error {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s  %s\n", e.SHA256, e.RelPath)
	}
	return os.WriteFile(filepath.Join(root, SumsName), []byte(b.String()), 0o644)
}

// WriteDeltaManifest writes a MANIFEST.tsv into the delta root describing only
// the artifacts added during this run, so merge.sh can fold it into the
// permanent mirror.
func (s *Store) WriteDeltaManifest() error {
	if s.Delta == "" {
		return nil
	}
	var b strings.Builder
	for _, e := range s.Added() {
		b.WriteString(e.line())
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(s.Delta, ManifestName), []byte(b.String()), 0o644)
}

func linkOrCopy(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".airgap-*.part")
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		os.Remove(out.Name())
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dst)
}

// MirrorPath copies generated metadata (apt indexes, the PyPI simple index,
// OCI layout sidecars) into the delta root. These files are not artifacts, so
// they are not recorded in the manifest, but the delta must still carry them
// or the air-gapped mirror would be left with stale metadata after a merge.
func (s *Store) MirrorPath(rel string) error {
	if s.Delta == "" {
		return nil
	}
	src := s.Abs(rel)
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return copyFile(src, filepath.Join(s.Delta, filepath.FromSlash(rel)))
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		sub, err := filepath.Rel(s.Root, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(s.Delta, sub)
		// The OCI layout directories contain a relative "blobs" symlink into
		// the shared blob store; recreate it rather than copying through it.
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if _, err := os.Lstat(dst); err == nil {
				return nil
			}
			return os.Symlink(target, dst)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, dst)
	})
}

// copyFile always overwrites, because regenerated metadata must replace the
// previous version.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
