package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"oroborus.dev/orotools/internal/manifest"
)

// Internal prefixes and ownership marker for staging/backup directories.
// A crashed run leaves orphans behind; the next run removes only directories
// that carry the internal prefix (NFR-4).
const (
	stagePrefix  = ".orotools-stage-"
	backupPrefix = ".orotools-backup-"
	ownerMarker  = ".orotools-owner.json"
)

// ErrDestinationExists reports a destination directory that exists without
// lock ownership — never silently adopted or overwritten (FR-14).
var ErrDestinationExists = errors.New("destino já existe sem registro no lock")

// Service performs managed skill operations inside a project (skills-manager
// design: transactions per skill, structured results, no direct printing).
type Service struct {
	Base         string // project root
	Agent        string // canonical agent id
	ManifestPath string // orotools.yaml path
	LockPath     string // .orotools/skills.lock.yaml path
}

// TargetFor returns the project-relative destination directory for a skill
// name, derived from the configured agent. The name must be a single clean
// path segment: remote frontmatter is untrusted input, so traversal,
// separators and internal prefixes are rejected before any path is formed
// (skills-manager FR-13).
func (s Service) TargetFor(name string) (string, error) {
	if err := validateDestinationName(name); err != nil {
		return "", err
	}
	dir, err := SkillsDir(s.Agent)
	if err != nil {
		return "", err
	}
	return path.Join(dir, name), nil
}

// validateDestinationName enforces a safe basename for a skill destination.
func validateDestinationName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("nome de destino vazio")
	case !cleanSegment(name):
		return fmt.Errorf("nome de destino %q não é um segmento de caminho simples", name)
	case strings.HasPrefix(name, stagePrefix), strings.HasPrefix(name, backupPrefix):
		return fmt.Errorf("nome de destino %q usa o prefixo interno reservado", name)
	}
	return nil
}

// cleanSegment reports whether s is a single, clean, traversal-free path
// segment usable as a directory name.
func cleanSegment(s string) bool {
	if s == "" || strings.ContainsAny(s, `/\`) || s == "." || s == ".." {
		return false
	}
	return path.Clean(s) == s
}

// InstallResult reports the outcome of one installation.
type InstallResult struct {
	ID      string
	Name    string
	Version string
	Target  string
	Digest  string
	Already bool // re-install with identical content; zero mutation
}

// Install stages the candidate and swaps it into the target directory,
// persisting manifest and lock only after the swap (design transaction).
// GitHub candidates are recorded in skills.managed (promoting the manifest to
// v2); bundled candidates stay only in skills.bundled (FR-15). The
// destination is create-only: an existing unowned directory is a conflict.
func (s Service) Install(m *manifest.Manifest, lock *Lock, cand Candidate) (InstallResult, error) {
	targetRel, err := s.TargetFor(cand.Name)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: %w", cand.ID, err)
	}
	destAbs, err := safeJoin(s.Base, targetRel)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: %w", cand.ID, err)
	}

	staging, stagingDigest, err := s.stage(cand)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: stage: %w", cand.ID, err)
	}

	result := InstallResult{
		ID:      cand.ID,
		Name:    cand.Name,
		Version: cand.Version.String(),
		Target:  targetRel,
		Digest:  stagingDigest,
	}

	destExists := pathExists(destAbs)
	if destExists {
		os.RemoveAll(staging)
		entry, owned := lock.Lookup(cand.ID)
		destDigest, digestErr := Digest(os.DirFS(s.Base), targetRel)
		if owned && entry.Target == targetRel && digestErr == nil &&
			destDigest == entry.ContentSHA256 && stagingDigest == entry.ContentSHA256 {
			result.Already = true
			return result, nil
		}
		return InstallResult{}, fmt.Errorf("install %s: %w: %s", cand.ID, ErrDestinationExists, targetRel)
	}

	if err := os.Rename(staging, destAbs); err != nil {
		os.RemoveAll(staging)
		return InstallResult{}, fmt.Errorf("install %s: replace: %w", cand.ID, err)
	}

	mNext := cloneManifest(m)
	if cand.Source == SourceGitHub && mNext != nil {
		mNext.AddManagedSkill(fmt.Sprintf("%s:%s", cand.Source, cand.SourceRef), cand.Name)
	}
	lockNext := cloneLock(lock)
	lockNext.Upsert(LockEntry{
		ID:            cand.ID,
		Name:          cand.Name,
		Source:        string(cand.Source),
		SourceRef:     cand.SourceRef,
		Path:          cand.Path,
		Version:       cand.Version.String(),
		Revision:      cand.Revision,
		Target:        targetRel,
		ContentSHA256: stagingDigest,
	})
	if err := s.persistState(mNext, lockNext); err != nil {
		os.RemoveAll(destAbs) // rollback the swap; state files were restored
		return InstallResult{}, fmt.Errorf("install %s: persist: %w", cand.ID, err)
	}
	if mNext != nil && m != nil {
		*m = *mNext
	}
	*lock = *lockNext
	return result, nil
}

// stage copies the candidate tree into a staging directory under the skills
// parent, marked with an ownership marker, and returns its absolute path and
// canonical digest.
func (s Service) stage(cand Candidate) (string, string, error) {
	skillsRel, err := SkillsDir(s.Agent)
	if err != nil {
		return "", "", err
	}
	parentAbs, err := safeJoin(s.Base, skillsRel)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(parentAbs, 0o755); err != nil {
		return "", "", err
	}
	staging, err := os.MkdirTemp(parentAbs, stagePrefix+slugify(cand.ID)+"-")
	if err != nil {
		return "", "", err
	}
	writeOwnerMarker(staging, cand.ID)
	if err := copyTree(cand.Tree.FS, cand.Tree.Root, staging); err != nil {
		os.RemoveAll(staging)
		return "", "", err
	}
	// The marker only tags the staging for crash cleanup; it must never reach
	// the installed target nor pollute the canonical digest.
	os.Remove(filepath.Join(staging, ownerMarker))
	stagingDigest, err := Digest(os.DirFS(filepath.Dir(staging)), filepath.Base(staging))
	if err != nil {
		os.RemoveAll(staging)
		return "", "", err
	}
	return staging, stagingDigest, nil
}

// replace swaps an already-staged candidate into an owned target (update
// flow): the current directory becomes a private backup, the staging becomes
// the target, and the backup is removed only after manifest/lock persistence
// succeeds (design transaction, FR-29). On any failure the previous directory
// and state files are restored.
func (s Service) replace(m *manifest.Manifest, lock *Lock, cand Candidate, entry LockEntry, staging, stagingDigest string, mNext *manifest.Manifest) error {
	targetRel := entry.Target
	destAbs, err := safeJoin(s.Base, targetRel)
	if err != nil {
		os.RemoveAll(staging)
		return fmt.Errorf("replace: %w", err)
	}
	// A missing destination (reinstall after deletion) has nothing to back up;
	// the staging is swapped straight in.
	backup := ""
	if pathExists(destAbs) {
		parentAbs := filepath.Dir(destAbs)
		backup, err = os.MkdirTemp(parentAbs, backupPrefix+slugify(cand.ID)+"-")
		if err != nil {
			os.RemoveAll(staging)
			return fmt.Errorf("backup: %w", err)
		}
		// MkdirTemp creates the directory; rename needs the name free.
		if err := os.Remove(backup); err != nil {
			os.RemoveAll(staging)
			return fmt.Errorf("backup: %w", err)
		}
		if err := os.Rename(destAbs, backup); err != nil {
			os.RemoveAll(staging)
			return fmt.Errorf("backup: %w", err)
		}
		// Tag the backup so a crash before cleanup leaves an ownership trail
		// the next run can verify (design: prefix + ownership verificável).
		writeOwnerMarker(backup, cand.ID)
	}
	// restoreBackup rolls the previous content back after a failure. The
	// ownership marker is stripped first: it is bookkeeping, not skill
	// content — the rollback must return the exact previous bytes (FR-29).
	restoreBackup := func() {
		if backup == "" {
			return
		}
		os.Remove(filepath.Join(backup, ownerMarker))
		os.Rename(backup, destAbs)
	}
	if err := os.Rename(staging, destAbs); err != nil {
		restoreBackup()
		os.RemoveAll(staging)
		return fmt.Errorf("swap: %w", err)
	}
	lockNext := cloneLock(lock)
	entry.Version = cand.Version.String()
	entry.Revision = cand.Revision
	entry.ContentSHA256 = stagingDigest
	lockNext.Upsert(entry)
	if err := s.persistState(mNext, lockNext); err != nil {
		os.RemoveAll(destAbs)
		restoreBackup()
		return fmt.Errorf("persist: %w", err)
	}
	if backup != "" {
		os.RemoveAll(backup)
	}
	if mNext != nil && m != nil {
		*m = *mNext
	}
	*lock = *lockNext
	return nil
}

// persistState writes manifest (when non-nil) and lock, restoring the
// previous bytes of any state file it already wrote when a later step fails
// (design transaction step 6).
func (s Service) persistState(mNext *manifest.Manifest, lockNext *Lock) error {
	prevManifest := readBytesIfExists(s.ManifestPath)
	prevLock := readBytesIfExists(s.LockPath)
	if mNext != nil {
		if err := manifest.Write(s.ManifestPath, mNext); err != nil {
			return err
		}
	}
	if err := lockNext.Save(s.LockPath); err != nil {
		if prevManifest != nil {
			os.WriteFile(s.ManifestPath, prevManifest, 0o644)
		}
		return err
	}
	_ = prevLock
	return nil
}

// CleanOrphans removes staging and backup directories left by crashed runs
// under every known agent skills directory. Only directories carrying the
// internal prefixes are touched.
func (s Service) CleanOrphans() {
	for _, agent := range []string{"opencode", "cursor", "claude-code", "codex", "copilot"} {
		skillsRel, err := SkillsDir(agent)
		if err != nil {
			continue
		}
		parentAbs, err := safeJoin(s.Base, skillsRel)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(parentAbs)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, stagePrefix) && !strings.HasPrefix(name, backupPrefix) {
				continue
			}
			if !e.IsDir() {
				continue
			}
			// Internal prefix alone is not ownership: only remove directories
			// with a verifiable marker (design / review 2026-09-09).
			if !ownedByUs(filepath.Join(parentAbs, name)) {
				continue
			}
			os.RemoveAll(filepath.Join(parentAbs, name))
		}
	}
}

// writeOwnerMarker tags dir as an Oro-managed staging/backup directory.
// Best-effort: failure only means the directory will not be auto-cleaned.
func writeOwnerMarker(dir, skillID string) {
	if src, err := json.Marshal(map[string]string{"skill": skillID}); err == nil {
		os.WriteFile(filepath.Join(dir, ownerMarker), src, 0o644)
	}
}

// ownedByUs reports whether dir carries a parseable, non-empty ownership
// marker. Directories without one are left untouched by CleanOrphans.
func ownedByUs(dir string) bool {
	src, err := os.ReadFile(filepath.Join(dir, ownerMarker))
	if err != nil {
		return false
	}
	var marker struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal(src, &marker); err != nil || marker.Skill == "" {
		return false
	}
	return true
}

// copyTree copies a whole skill tree (regular files and directories only)
// into destDir, preserving the executable bit and rejecting symlinks and
// special file types (FR-13). No code from the skill is ever executed (NFR-3).
func copyTree(srcFS fs.FS, root, destDir string) error {
	return fs.WalkDir(srcFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := relFromRoot(root, p)
		if err != nil {
			return err
		}
		if rel == "" {
			return nil // the root itself
		}
		if d.IsDir() {
			return nil // created implicitly when copying files
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("arquivo %q não é regular", p)
		}
		target := filepath.Join(destDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		src, err := srcFS.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		return dst.Close()
	})
}

func cloneManifest(m *manifest.Manifest) *manifest.Manifest {
	if m == nil {
		return nil
	}
	c := *m
	c.Skills.Bundled = append([]string(nil), m.Skills.Bundled...)
	c.Skills.External = append([]string(nil), m.Skills.External...)
	c.Skills.Managed = append([]manifest.ManagedSkillRef(nil), m.Skills.Managed...)
	c.Agents.Bundled = append([]string(nil), m.Agents.Bundled...)
	c.Agents.External = append([]string(nil), m.Agents.External...)
	if m.Variables != nil {
		c.Variables = make(map[string]any, len(m.Variables))
		for k, v := range m.Variables {
			c.Variables[k] = v
		}
	}
	return &c
}

func cloneLock(l *Lock) *Lock {
	c := *l
	c.Skills = append([]LockEntry(nil), l.Skills...)
	return &c
}

func safeJoin(base, dest string) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("destination is empty")
	}
	joined := filepath.Join(base, filepath.FromSlash(dest))
	rel, err := filepath.Rel(base, joined)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be made relative to base %q: %w", dest, base, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base %q", dest, base)
	}
	return joined, nil
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func readBytesIfExists(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// slugify reduces an ID to a filesystem-safe fragment for staging names.
func slugify(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}
