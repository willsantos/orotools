package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// LockDir and LockFilename locate the skills lock inside the project root
// (skills-manager FR-18): .orotools/skills.lock.yaml.
const (
	LockDir      = ".orotools"
	LockFilename = "skills.lock.yaml"
)

// LockHeader is written as a comment at the top of every written lock.
const LockHeader = "# .orotools/skills.lock.yaml — estado instalado das skills gerenciadas. Não edite manualmente."

// ErrLockNotFound reports that the project has no skills lock yet.
var ErrLockNotFound = errors.New("skills lock não encontrado")

// LockEntry is one managed skill record. No timestamps, no absolute paths —
// the lock is deterministic and meant to be committed (skills-manager FR-18).
type LockEntry struct {
	ID            string `yaml:"id"`
	Name          string `yaml:"name"`
	Source        string `yaml:"source"`     // recipe | github (SourceKind)
	SourceRef     string `yaml:"source_ref"` // recipe name or repository slug
	Path          string `yaml:"path"`       // skill path inside the source
	Version       string `yaml:"version"`    // installed version (metadata.version)
	Revision      string `yaml:"revision"`   // commit SHA of the run; empty for bundled
	Target        string `yaml:"target"`     // project-relative destination directory
	ContentSHA256 string `yaml:"content_sha256"`
}

// Lock models .orotools/skills.lock.yaml schema v1.
type Lock struct {
	Version int         `yaml:"version"`
	Skills  []LockEntry `yaml:"skills"`
}

// NewLock returns an empty v1 lock.
func NewLock() *Lock {
	return &Lock{Version: 1}
}

// ReadLock loads and validates the lock at path. A missing file reports
// ErrLockNotFound.
func ReadLock(path string) (*Lock, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrLockNotFound, path)
		}
		return nil, fmt.Errorf("read skills lock %q: %w", path, err)
	}
	lock, err := ParseLock(src)
	if err != nil {
		return nil, fmt.Errorf("parse skills lock %q: %w", path, err)
	}
	return lock, nil
}

// ParseLock decodes src as a Lock schema v1. Unknown fields, duplicate IDs and
// invalid records are errors (strict).
func ParseLock(src []byte) (*Lock, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(src)))
	dec.KnownFields(true)
	var lock Lock
	if err := dec.Decode(&lock); err != nil {
		return nil, err
	}
	if lock.Version != 1 {
		return nil, fmt.Errorf("skills lock version: must be 1, got %d", lock.Version)
	}
	seen := make(map[string]bool, len(lock.Skills))
	for i := range lock.Skills {
		e := &lock.Skills[i]
		if err := validateLockEntry(e); err != nil {
			return nil, err
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("skills lock: id duplicado %q", e.ID)
		}
		seen[e.ID] = true
	}
	sort.Slice(lock.Skills, func(i, j int) bool { return lock.Skills[i].ID < lock.Skills[j].ID })
	return &lock, nil
}

func validateLockEntry(e *LockEntry) error {
	for field, v := range map[string]string{
		"id":             e.ID,
		"name":           e.Name,
		"source":         e.Source,
		"source_ref":     e.SourceRef,
		"path":           e.Path,
		"version":        e.Version,
		"target":         e.Target,
		"content_sha256": e.ContentSHA256,
	} {
		if v == "" {
			return fmt.Errorf("skills lock: campo %q é obrigatório (id %q)", field, e.ID)
		}
	}
	if _, err := semver.StrictNewVersion(e.Version); err != nil {
		return fmt.Errorf("skills lock: version %q inválido (id %q): %w", e.Version, e.ID, err)
	}
	if err := checkRelPath(e.Target); err != nil {
		return fmt.Errorf("skills lock: target inválido (id %q): %w", e.ID, err)
	}
	if strings.HasPrefix(e.Target, "/") || filepath.IsAbs(e.Target) {
		return fmt.Errorf("skills lock: target %q deve ser relativo (id %q)", e.Target, e.ID)
	}
	return nil
}

// Lookup returns the record with the given ID.
func (l *Lock) Lookup(id string) (LockEntry, bool) {
	for _, e := range l.Skills {
		if e.ID == id {
			return e, true
		}
	}
	return LockEntry{}, false
}

// Upsert inserts or replaces the record for entry.ID keeping the list sorted.
func (l *Lock) Upsert(entry LockEntry) {
	for i := range l.Skills {
		if l.Skills[i].ID == entry.ID {
			l.Skills[i] = entry
			return
		}
	}
	l.Skills = append(l.Skills, entry)
	sort.Slice(l.Skills, func(i, j int) bool { return l.Skills[i].ID < l.Skills[j].ID })
}

// Save serialises the lock deterministically and writes it atomically:
// temporary file in the same directory, fsync, then rename. The file is
// written with 0644 and its directory created with 0755 (design).
func (l *Lock) Save(path string) error {
	if l == nil {
		return errors.New("skills lock: nothing to save (nil)")
	}
	if l.Version != 1 {
		return fmt.Errorf("skills lock version: must be 1, got %d", l.Version)
	}
	entries := make([]LockEntry, len(l.Skills))
	copy(entries, l.Skills)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	out, err := yaml.Marshal(&Lock{Version: l.Version, Skills: entries})
	if err != nil {
		return fmt.Errorf("marshal skills lock: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create skills lock directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".skills.lock-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary skills lock: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if err := writeAndSync(tmp, append([]byte(LockHeader+"\n"), out...)); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod skills lock: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename skills lock %q: %w", path, err)
	}
	return nil
}

func writeAndSync(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write skills lock: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync skills lock: %w", err)
	}
	return f.Close()
}
