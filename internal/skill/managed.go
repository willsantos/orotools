package skill

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"oroborus.dev/orotools/internal/manifest"
)

// Internal prefixes and ownership marker for staging/backup directories.
// A crashed run leaves orphans behind; the next run removes only directories
// that carry the internal prefix (NFR-4).
const (
	stagePrefix         = ".orotools-stage-"
	backupPrefix        = ".orotools-backup-"
	ownerMarker         = ".orotools-owner.json"
	transactionFilename = ".skills-transaction.json"
)

type operationJournal struct {
	Target      string `json:"target"`
	Stage       string `json:"stage,omitempty"`
	Backup      string `json:"backup,omitempty"`
	OldManifest string `json:"oldManifest,omitempty"`
	HadManifest bool   `json:"hadManifest"`
	OldLock     string `json:"oldLock,omitempty"`
	HadLock     bool   `json:"hadLock"`
}

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
	ID                      string
	Name                    string
	Version                 string
	Target                  string
	Digest                  string
	Already                 bool   // re-install with identical content; zero mutation
	ProjectStateFingerprint string `json:"projectStateFingerprint,omitempty"`
}

// Install stages the candidate and swaps it into the target directory,
// persisting manifest and lock only after the swap (design transaction).
// GitHub candidates are recorded in skills.managed (promoting the manifest to
// v2); bundled candidates stay only in skills.bundled (FR-15). The
// destination is create-only: an existing unowned directory is a conflict.
func (s Service) Install(m *manifest.Manifest, lock *Lock, cand Candidate) (InstallResult, error) {
	originalManifest, originalLock := m, lock
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	projectLock, err := AcquireProjectLock(ctx, s.Base)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: project lock: %w", cand.ID, err)
	}
	defer projectLock.Release()
	if err := s.recoverTransaction(); err != nil {
		return InstallResult{}, fmt.Errorf("recover interrupted skill transaction: %w", err)
	}
	m, lock, err = s.reloadState(m, lock)
	if err != nil {
		return InstallResult{}, err
	}
	result, err := s.installUnlocked(m, lock, cand)
	if err == nil {
		if originalManifest != nil && m != nil && originalManifest != m {
			*originalManifest = *m
		}
		if originalLock != nil && lock != nil && originalLock != lock {
			*originalLock = *lock
		}
	}
	return result, err
}

func (s Service) installUnlocked(m *manifest.Manifest, lock *Lock, cand Candidate, authorizedDigest ...string) (InstallResult, error) {
	return s.installUnlockedContext(context.Background(), m, lock, cand, authorizedDigest...)
}

func (s Service) installUnlockedContext(ctx context.Context, m *manifest.Manifest, lock *Lock, cand Candidate, authorizedDigest ...string) (InstallResult, error) {
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	if err := s.validateStatePaths(); err != nil {
		return InstallResult{}, fmt.Errorf("install %s: unsafe state path: %w", cand.ID, err)
	}
	targetRel, err := s.TargetFor(cand.Name)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: %w", cand.ID, err)
	}
	destAbs, err := safeJoin(s.Base, targetRel)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: %w", cand.ID, err)
	}

	staging, stagingDigest, err := s.stageContext(ctx, cand)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install %s: stage: %w", cand.ID, err)
	}
	if len(authorizedDigest) > 0 && stagingDigest != authorizedDigest[0] {
		os.RemoveAll(staging)
		return InstallResult{}, fmt.Errorf("install %s: staged digest differs from reviewed plan", cand.ID)
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
		entry, owned := lock.LookupTarget(cand.ID, targetRel)
		destDigest, digestErr := Digest(os.DirFS(s.Base), targetRel)
		if owned && digestErr == nil &&
			destDigest == entry.ContentSHA256 && stagingDigest == entry.ContentSHA256 {
			result.Already = true
			return result, nil
		}
		return InstallResult{}, fmt.Errorf("install %s: %w: %s", cand.ID, ErrDestinationExists, targetRel)
	}
	journal, err := s.beginTransaction(targetRel, staging, "")
	if err != nil {
		os.RemoveAll(staging)
		return InstallResult{}, err
	}
	if err := ctx.Err(); err != nil {
		os.RemoveAll(staging)
		_ = s.clearTransaction()
		return InstallResult{}, err
	}

	if err := os.Rename(staging, destAbs); err != nil {
		os.RemoveAll(staging)
		_ = s.clearTransaction()
		return InstallResult{}, fmt.Errorf("install %s: replace: %w", cand.ID, err)
	}
	if err := ctx.Err(); err != nil {
		if rollbackErr := s.rollbackTransaction(journal, destAbs, ""); rollbackErr != nil {
			return InstallResult{}, fmt.Errorf("install canceled: %v; rollback failed: %w", err, rollbackErr)
		}
		return InstallResult{}, err
	}

	mNext := cloneManifest(m)
	if cand.Source == SourceGitHub && mNext != nil {
		mNext.AddManagedSkill(fmt.Sprintf("%s:%s", cand.Source, cand.SourceRef), cand.Name)
	}
	lockNext := cloneLock(lock)
	lockNext.Upsert(LockEntry{
		ID:              cand.ID,
		Name:            cand.Name,
		Source:          string(cand.Source),
		SourceRef:       cand.SourceRef,
		Path:            cand.Path,
		Version:         cand.Version.String(),
		Revision:        cand.Revision,
		Target:          targetRel,
		ContentSHA256:   stagingDigest,
		DigestAlgorithm: DigestAlgorithm,
	})
	if err := s.persistState(mNext, lockNext); err != nil {
		if rollbackErr := s.rollbackTransaction(journal, destAbs, ""); rollbackErr != nil {
			return InstallResult{}, fmt.Errorf("install %s: persist failed: %v; rollback failed: %w", cand.ID, err, rollbackErr)
		}
		return InstallResult{}, fmt.Errorf("install %s: persist: %w", cand.ID, err)
	}
	if err := s.clearTransaction(); err != nil {
		return InstallResult{}, fmt.Errorf("install %s: transaction cleanup: %w", cand.ID, err)
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
	return s.stageContext(context.Background(), cand)
}

func (s Service) stageContext(ctx context.Context, cand Candidate) (string, string, error) {
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
	if err := copyTreeContext(ctx, cand.Tree.FS, cand.Tree.Root, staging); err != nil {
		os.RemoveAll(staging)
		return "", "", err
	}
	// copyTree rejects source entries using the reserved marker name, so this
	// marker can only be OroTools bookkeeping and is excluded from digest.
	if err := os.Remove(filepath.Join(staging, ownerMarker)); err != nil {
		os.RemoveAll(staging)
		return "", "", fmt.Errorf("remove staging owner marker: %w", err)
	}
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
		backup = filepath.Join(filepath.Dir(destAbs), backupPrefix+slugify(cand.ID)+"-"+fmt.Sprint(time.Now().UnixNano()))
	}
	journal, err := s.beginTransaction(targetRel, staging, backup)
	if err != nil {
		os.RemoveAll(staging)
		return err
	}
	if pathExists(destAbs) {
		if err := os.Rename(destAbs, backup); err != nil {
			os.RemoveAll(staging)
			_ = s.clearTransaction()
			return fmt.Errorf("backup: %w", err)
		}
	}
	if err := os.Rename(staging, destAbs); err != nil {
		if rollbackErr := s.rollbackTransaction(journal, destAbs, backup); rollbackErr != nil {
			return fmt.Errorf("swap failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("swap: %w", err)
	}
	lockNext := cloneLock(lock)
	entry.Version = cand.Version.String()
	entry.Revision = cand.Revision
	entry.ContentSHA256 = stagingDigest
	entry.DigestAlgorithm = DigestAlgorithm
	lockNext.Upsert(entry)
	if err := s.persistState(mNext, lockNext); err != nil {
		if rollbackErr := s.rollbackTransaction(journal, destAbs, backup); rollbackErr != nil {
			return fmt.Errorf("persist failed: %v; rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("persist: %w", err)
	}
	if err := s.clearTransaction(); err != nil {
		return fmt.Errorf("clear operation journal: %w", err)
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
	prevManifest, manifestErr := os.ReadFile(s.ManifestPath)
	if manifestErr != nil && !os.IsNotExist(manifestErr) {
		return manifestErr
	}
	if mNext != nil {
		if err := manifest.Write(s.ManifestPath, mNext); err != nil {
			return err
		}
	}
	if err := lockNext.Save(s.LockPath); err != nil {
		if manifestErr == nil {
			if err := restoreAtomic(s.ManifestPath, prevManifest); err != nil {
				return fmt.Errorf("save lock failed and manifest rollback failed: %w", err)
			}
		} else {
			if err := os.Remove(s.ManifestPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("save lock failed and manifest rollback failed: %w", err)
			}
		}
		return err
	}
	return nil
}

func (s Service) beginTransaction(target, stage, backup string) (operationJournal, error) {
	manifestBytes, manifestErr := os.ReadFile(s.ManifestPath)
	if manifestErr != nil && !os.IsNotExist(manifestErr) {
		return operationJournal{}, manifestErr
	}
	lockBytes, lockErr := os.ReadFile(s.LockPath)
	if lockErr != nil && !os.IsNotExist(lockErr) {
		return operationJournal{}, lockErr
	}
	j := operationJournal{Target: target, Stage: stage, Backup: backup, HadManifest: manifestErr == nil, HadLock: lockErr == nil}
	if j.HadManifest {
		j.OldManifest = base64.StdEncoding.EncodeToString(manifestBytes)
	}
	if j.HadLock {
		j.OldLock = base64.StdEncoding.EncodeToString(lockBytes)
	}
	data, err := json.Marshal(j)
	if err != nil {
		return operationJournal{}, err
	}
	journalPath := filepath.Join(s.Base, LockDir, transactionFilename)
	if err := atomicFile(journalPath, append(data, '\n'), 0o600); err != nil {
		return operationJournal{}, fmt.Errorf("write operation journal: %w", err)
	}
	return j, nil
}

func (s Service) clearTransaction() error {
	err := os.Remove(filepath.Join(s.Base, LockDir, transactionFilename))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s Service) recoverTransaction() error {
	journalPath := filepath.Join(s.Base, LockDir, transactionFilename)
	_, err := os.Lstat(journalPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("pending skill transaction at %s; automatic recovery is disabled; inspect and recover explicitly", journalPath)
}

// rollbackTransaction is used only by the live operation holding writer lock;
// it uses in-memory paths captured by that operation, never a journal read from
// disk. A leftover journal after rollback failure blocks future writers.
func (s Service) rollbackTransaction(j operationJournal, target, backup string) error {
	if backup != "" && pathExists(backup) {
		if pathExists(target) {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
		if err := os.Rename(backup, target); err != nil {
			return err
		}
	} else if backup == "" && pathExists(target) {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	if j.HadManifest {
		b, err := base64.StdEncoding.DecodeString(j.OldManifest)
		if err != nil {
			return err
		}
		if err := restoreAtomic(s.ManifestPath, b); err != nil {
			return err
		}
	} else if err := os.Remove(s.ManifestPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if j.HadLock {
		b, err := base64.StdEncoding.DecodeString(j.OldLock)
		if err != nil {
			return err
		}
		if err := restoreAtomic(s.LockPath, b); err != nil {
			return err
		}
	} else if err := os.Remove(s.LockPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if j.Stage != "" {
		if err := os.RemoveAll(j.Stage); err != nil {
			return err
		}
	}
	return s.clearTransaction()
}

func restoreAtomic(path string, data []byte) error { return atomicFile(path, data, 0o644) }

func atomicFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".orotools-state-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CleanOrphans acquires the project writer lock, recovers any journaled swap,
// then removes only marked staging directories. Backups without transaction
// evidence are deliberately retained for manual recovery.
func (s Service) CleanOrphans() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	projectLock, err := AcquireProjectLock(ctx, s.Base)
	if err != nil {
		return
	}
	defer projectLock.Release()
	// Reconcile any interrupted file/directory transaction before cleanup.
	if err := s.recoverTransaction(); err != nil {
		return
	}
	for _, agent := range AllAgents {
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
			if !strings.HasPrefix(name, stagePrefix) {
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

func (s Service) reloadState(m *manifest.Manifest, lock *Lock) (*manifest.Manifest, *Lock, error) {
	if s.ManifestPath != "" {
		loaded, err := manifest.Read(s.ManifestPath)
		if err == nil {
			m = loaded
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("reload manifest: %w", err)
		} else {
			m = nil
		}
	}
	if s.LockPath != "" {
		loaded, err := ReadLock(s.LockPath)
		if err == nil {
			lock = loaded
		} else if errors.Is(err, ErrLockNotFound) {
			lock = NewLock()
		} else {
			return nil, nil, fmt.Errorf("reload lock: %w", err)
		}
	}
	if lock == nil {
		lock = NewLock()
	}
	return m, lock, nil
}

func (s Service) stateFingerprint() (string, error) {
	h := sha256.New()
	for _, path := range []string{s.ManifestPath, s.LockPath} {
		if path == "" {
			_, _ = h.Write([]byte("no-path\x00"))
			continue
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			_, _ = h.Write([]byte("missing\x00"))
			continue
		}
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte("present\x00"))
		_, _ = h.Write([]byte(filepath.Clean(path)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// ProjectStateFingerprint exposes the manifest+lock snapshot used by install
// plans so callers can safely sequence multiple approved applies without
// silently accepting intervening project-state changes.
func (s Service) ProjectStateFingerprint() (string, error) {
	return s.stateFingerprint()
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
func copyTreeContext(ctx context.Context, srcFS fs.FS, root, destDir string) error {
	return fs.WalkDir(srcFS, root, func(p string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
		if path.Base(p) == ownerMarker {
			return fmt.Errorf("source path %q uses reserved internal marker", p)
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
		if _, err := io.Copy(dst, contextReader{ctx: ctx, reader: src}); err != nil {
			dst.Close()
			return err
		}
		return dst.Close()
	})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
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
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve project root %q: %w", base, err)
	}
	baseReal, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		return "", fmt.Errorf("resolve project root %q: %w", baseAbs, err)
	}
	joined := filepath.Join(baseReal, filepath.FromSlash(dest))
	rel, err := filepath.Rel(baseReal, joined)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be made relative to base %q: %w", dest, base, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base %q", dest, base)
	}
	current := baseReal
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		if segment == "." || segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if errors.Is(err, syscall.ENOTDIR) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("inspect path component %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink path component rejected: %s", current)
		}
	}
	return joined, nil
}

// SafeProjectPath resolves a project-relative path and rejects symlinked path
// components. Callers use it before reading or writing project state files.
func SafeProjectPath(projectRoot, relative string) (string, error) {
	return safeJoin(projectRoot, relative)
}

func (s Service) validateStatePaths() error {
	base, err := filepath.Abs(s.Base)
	if err != nil {
		return err
	}
	if s.ManifestPath != "" {
		manifestPath, err := filepath.Abs(s.ManifestPath)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, manifestPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("manifest path is outside project root")
		}
		if _, err := safeJoin(s.Base, filepath.ToSlash(relative)); err != nil {
			return fmt.Errorf("manifest path: %w", err)
		}
	}
	if s.LockPath != "" {
		lockPath, err := filepath.Abs(s.LockPath)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, lockPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("lock path is outside project root")
		}
		if _, err := safeJoin(s.Base, filepath.ToSlash(relative)); err != nil {
			return fmt.Errorf("lock path: %w", err)
		}
	}
	return nil
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
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
