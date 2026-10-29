package skill

import (
	"context"
	"fmt"
	"os"
	"time"

	"oroborus.dev/orotools/internal/manifest"
)

// AgentFromManifest returns manifest.ai.agent, defaulting to "opencode" for
// legacy manifests without the field (skills-manager FR-2).
func AgentFromManifest(m *manifest.Manifest) string {
	if m == nil || m.AI.Agent == "" {
		return "opencode"
	}
	return m.AI.Agent
}

// MigrationState classifies a legacy bundled skill against the current
// project state (skills-manager FR-20).
type MigrationState string

const (
	// MigrationManaged: the lock already owns the skill; nothing to do.
	MigrationManaged MigrationState = "gerenciada"
	// MigrationAdopted: target content matches the bundled source; the skill
	// was registered in the lock without touching the target.
	MigrationAdopted MigrationState = "adotada"
	// MigrationMissing: declared but not installed; it stays installable in
	// the wizard and absent from the lock.
	MigrationMissing MigrationState = "declarada, não instalada"
	// MigrationDiverged: target exists with different content; ownership is
	// never adopted automatically — update requires --force.
	MigrationDiverged MigrationState = "não gerenciada (modificada)"
)

// MigrationResult reports the classification of one legacy bundled ref.
type MigrationResult struct {
	ID     string
	Name   string
	Ref    string
	State  MigrationState
	Target string
	Entry  LockEntry // meaningful for Managed and Adopted
}

// MigrateBundled lazily adopts legacy bundled skills into the lock: a target
// whose content matches the current bundled source is registered without any
// modification to the target or the manifest (FR-20). Absent and divergent
// targets are only classified, never mutated. skills.external is out of scope
// by construction: only bundled candidates reach this function.
func (s Service) MigrateBundled(lock *Lock, cands []Candidate) ([]MigrationResult, error) {
	results, _, err := s.MigrateBundledApproved(lock, cands, "", nil)
	return results, err
}

// MigrateBundledApproved performs only the migration preview already consented
// to by caller. expectedFingerprint is checked under project lock before and
// after recovery; approvedResults bind target classifications/digests to the
// read-only preview. Returned fingerprint is captured before releasing lock.
func (s Service) MigrateBundledApproved(lock *Lock, cands []Candidate, expectedFingerprint string, approvedResults []MigrationResult) ([]MigrationResult, string, error) {
	if len(cands) == 0 && expectedFingerprint == "" {
		return nil, "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	projectLock, err := AcquireProjectLock(ctx, s.Base)
	if err != nil {
		return nil, "", fmt.Errorf("migrate: project lock: %w", err)
	}
	defer projectLock.Release()
	if expectedFingerprint != "" {
		fingerprint, fingerprintErr := s.stateFingerprint()
		if fingerprintErr != nil {
			return nil, "", fmt.Errorf("migrate: inspect expected project state: %w", fingerprintErr)
		}
		if fingerprint != expectedFingerprint {
			return nil, "", fmt.Errorf("migrate: project state changed since preview")
		}
	}
	if err := s.recoverTransaction(); err != nil {
		return nil, "", fmt.Errorf("recover interrupted skill transaction: %w", err)
	}
	if expectedFingerprint != "" {
		fingerprint, fingerprintErr := s.stateFingerprint()
		if fingerprintErr != nil {
			return nil, "", fingerprintErr
		}
		if fingerprint != expectedFingerprint {
			return nil, "", fmt.Errorf("migrate: recovery changed project state; create a new preview")
		}
	}
	originalLock := lock
	_, lock, err = s.reloadState(nil, lock)
	if err != nil {
		return nil, "", err
	}
	previewResults, _, err := s.InspectBundledMigration(lock, cands)
	if err != nil {
		return nil, "", err
	}
	if expectedFingerprint != "" && !sameMigrationPreview(previewResults, approvedResults) {
		return nil, "", fmt.Errorf("migrate: bundled targets changed since preview; create a new preview")
	}
	results, err := s.migrateBundledUnlocked(lock, cands)
	if err == nil && originalLock != nil && originalLock != lock {
		*originalLock = *lock
	}
	if err != nil {
		return nil, "", err
	}
	fingerprint, err := s.stateFingerprint()
	if err != nil {
		return nil, "", fmt.Errorf("migrate: fingerprint post-state: %w", err)
	}
	return results, fingerprint, nil
}

func sameMigrationPreview(a, b []MigrationResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s Service) migrateBundledUnlocked(lock *Lock, cands []Candidate) ([]MigrationResult, error) {
	results, projected, err := s.InspectBundledMigration(lock, cands)
	if err != nil {
		return nil, err
	}
	var adopted bool
	for _, result := range results {
		if result.State != MigrationAdopted {
			continue
		}
		lock.Upsert(result.Entry)
		adopted = true
	}
	if adopted {
		// Keep the projected result as source of truth and persist only after
		// caller has explicitly entered an operation that may mutate project state.
		*lock = *projected
		if err := lock.Save(s.LockPath); err != nil {
			return nil, fmt.Errorf("migrate: persistir lock: %w", err)
		}
	}
	return results, nil
}

// InspectBundledMigration classifies legacy bundled targets without creating
// or rewriting the project lock. The returned projected lock includes intact
// legacy targets so read-only previews can report them as installed.
func (s Service) InspectBundledMigration(lock *Lock, cands []Candidate) ([]MigrationResult, *Lock, error) {
	if lock == nil {
		return nil, nil, fmt.Errorf("migrate: lock ausente")
	}
	if err := s.validateStatePaths(); err != nil {
		return nil, nil, fmt.Errorf("migrate: unsafe state path: %w", err)
	}
	projected := cloneLock(lock)
	results, err := s.inspectBundledUnlocked(lock, cands)
	if err != nil {
		return nil, nil, err
	}
	for _, result := range results {
		if result.State == MigrationAdopted {
			projected.Upsert(result.Entry)
		}
	}
	return results, projected, nil
}

func (s Service) inspectBundledUnlocked(lock *Lock, cands []Candidate) ([]MigrationResult, error) {
	if err := s.validateStatePaths(); err != nil {
		return nil, fmt.Errorf("migrate: unsafe state path: %w", err)
	}
	results := make([]MigrationResult, 0, len(cands))
	for _, cand := range cands {
		if cand.Source != SourceRecipe {
			return nil, fmt.Errorf("migrate %s: apenas skills bundled são migradas", cand.ID)
		}
		targetRel, err := s.TargetFor(cand.Name)
		if err != nil {
			return nil, fmt.Errorf("migrate %s: %w", cand.ID, err)
		}
		res := MigrationResult{ID: cand.ID, Name: cand.Name, Ref: cand.Path, Target: targetRel}
		if entry, ok := lock.Lookup(cand.ID); ok {
			res.State = MigrationManaged
			res.Entry = entry
			results = append(results, res)
			continue
		}
		destAbs, err := safeJoin(s.Base, targetRel)
		if err != nil {
			return nil, fmt.Errorf("migrate %s: %w", cand.ID, err)
		}
		if !pathExists(destAbs) {
			res.State = MigrationMissing
			results = append(results, res)
			continue
		}
		sourceDigest, err := Digest(cand.Tree.FS, cand.Tree.Root)
		if err != nil {
			return nil, fmt.Errorf("migrate %s: digest da origem: %w", cand.ID, err)
		}
		destDigest, err := Digest(os.DirFS(s.Base), targetRel)
		if err != nil {
			return nil, fmt.Errorf("migrate %s: digest do destino: %w", cand.ID, err)
		}
		if sourceDigest != destDigest {
			res.State = MigrationDiverged
			results = append(results, res)
			continue
		}
		res.State = MigrationAdopted
		res.Entry = LockEntry{
			ID:              cand.ID,
			Name:            cand.Name,
			Source:          string(cand.Source),
			SourceRef:       cand.SourceRef,
			Path:            cand.Path,
			Version:         cand.Version.String(),
			Target:          targetRel,
			ContentSHA256:   sourceDigest,
			DigestAlgorithm: DigestAlgorithm,
		}
		results = append(results, res)
	}
	return results, nil
}
