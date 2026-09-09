package skill

import (
	"fmt"
	"os"

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
	results := make([]MigrationResult, 0, len(cands))
	var adopted []MigrationResult
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
			ID:            cand.ID,
			Name:          cand.Name,
			Source:        string(cand.Source),
			SourceRef:     cand.SourceRef,
			Path:          cand.Path,
			Version:       cand.Version.String(),
			Target:        targetRel,
			ContentSHA256: sourceDigest,
		}
		lock.Upsert(res.Entry)
		adopted = append(adopted, res)
		results = append(results, res)
	}
	if len(adopted) > 0 {
		if err := lock.Save(s.LockPath); err != nil {
			return nil, fmt.Errorf("migrate: persistir lock: %w", err)
		}
	}
	return results, nil
}
