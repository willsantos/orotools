package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"oroborus.dev/orotools/internal/manifest"
)

type InstallPlan struct {
	ProtocolVersion   int        `json:"protocolVersion"`
	Operation         string     `json:"operation"`
	SkillID           string     `json:"skillId"`
	Name              string     `json:"name"`
	Source            SourceKind `json:"source"`
	SourceRef         string     `json:"sourceRef"`
	SourcePath        string     `json:"sourcePath"`
	SourceRevision    string     `json:"sourceRevision,omitempty"`
	Version           string     `json:"version"`
	Agent             string     `json:"agent"`
	ProjectRootDigest string     `json:"projectRootDigest"`
	StateFingerprint  string     `json:"stateFingerprint"`
	Target            string     `json:"target"`
	DigestAlgorithm   string     `json:"digestAlgorithm"`
	ContentDigest     string     `json:"contentDigest"`
	Decision          string     `json:"decision"`
	Reason            string     `json:"reason,omitempty"`
	PlanToken         string     `json:"planToken"`
}

// PlanInstall is read-only: it inspects source and destination trees but writes
// no project files. Apply recomputes this plan under the project lock.
func (s Service) PlanInstall(lock *Lock, candidate Candidate) (InstallPlan, error) {
	return s.planInstall(lock, candidate)
}

func (s Service) planInstall(lock *Lock, candidate Candidate) (InstallPlan, error) {
	if lock == nil {
		return InstallPlan{}, fmt.Errorf("plan install: lock ausente")
	}
	target, err := s.TargetFor(candidate.Name)
	if err != nil {
		return InstallPlan{}, fmt.Errorf("plan install %s: %w", candidate.ID, err)
	}
	stateFingerprint, err := s.stateFingerprint()
	if err != nil {
		return InstallPlan{}, fmt.Errorf("plan install %s: inspect project state: %w", candidate.ID, err)
	}
	dest, err := safeJoin(s.Base, target)
	if err != nil {
		return InstallPlan{}, fmt.Errorf("plan install %s: %w", candidate.ID, err)
	}
	digest, err := Digest(candidate.Tree.FS, candidate.Tree.Root)
	if err != nil {
		return InstallPlan{}, fmt.Errorf("plan install %s: digest da origem: %w", candidate.ID, err)
	}
	plan := InstallPlan{
		ProtocolVersion:   1,
		Operation:         "skills.install",
		SkillID:           candidate.ID,
		Name:              candidate.Name,
		Source:            candidate.Source,
		SourceRef:         candidate.SourceRef,
		SourcePath:        candidate.Path,
		SourceRevision:    candidate.Revision,
		Version:           candidate.Version.String(),
		Agent:             s.Agent,
		ProjectRootDigest: projectRootDigest(s.Base),
		StateFingerprint:  stateFingerprint,
		Target:            target,
		DigestAlgorithm:   DigestAlgorithm,
		ContentDigest:     digest,
	}
	info, statErr := os.Lstat(dest)
	if os.IsNotExist(statErr) {
		plan.Decision = "install"
	} else if statErr != nil {
		return InstallPlan{}, fmt.Errorf("plan install %s: inspect destination: %w", candidate.ID, statErr)
	} else if !info.IsDir() {
		plan.Decision, plan.Reason = "blocked", "destination exists and is not a directory"
	} else if entry, owned := lock.LookupTarget(candidate.ID, target); !owned {
		plan.Decision, plan.Reason = "blocked", ErrDestinationExists.Error()
	} else {
		destDigest, digestErr := Digest(os.DirFS(s.Base), target)
		switch {
		case digestErr != nil:
			plan.Decision, plan.Reason = "blocked", fmt.Sprintf("cannot verify installed destination: %v", digestErr)
		case destDigest != entry.ContentSHA256:
			plan.Decision, plan.Reason = "blocked", "local drift detected; update requires explicit update flow"
		case destDigest != digest:
			plan.Decision, plan.Reason = "blocked", "upstream content changed; update requires explicit update flow"
		case entry.Source != string(candidate.Source) || entry.SourceRef != candidate.SourceRef || entry.Path != candidate.Path:
			plan.Decision, plan.Reason = "blocked", "installed provenance differs from selected source"
		default:
			plan.Decision = "unchanged"
		}
	}
	tokenPayload := plan
	tokenPayload.PlanToken = ""
	encoded, err := json.Marshal(tokenPayload)
	if err != nil {
		return InstallPlan{}, fmt.Errorf("plan install: encode token: %w", err)
	}
	sum := sha256.Sum256(encoded)
	plan.PlanToken = "sha256:" + hex.EncodeToString(sum[:])
	return plan, nil
}

func projectRootDigest(root string) string {
	abs, err := filepath.Abs(root)
	if err == nil {
		if real, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
			abs = real
		}
	}
	return "sha256:" + digestString([]byte(filepath.Clean(abs)))
}

func digestString(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// ApplyPlannedInstall applies only the exact read-only plan reviewed by caller.
// Source revision/content and destination state are revalidated under lock.
func (s Service) ApplyPlannedInstall(m *manifest.Manifest, lock *Lock, candidate Candidate, expectedToken string) (InstallResult, string, error) {
	return s.ApplyPlannedInstallContext(context.Background(), m, lock, candidate, expectedToken)
}

func (s Service) ApplyPlannedInstallContext(ctx context.Context, m *manifest.Manifest, lock *Lock, candidate Candidate, expectedToken string) (InstallResult, string, error) {
	if err := ctx.Err(); err != nil {
		return InstallResult{}, "", fmt.Errorf("apply install canceled: %w", err)
	}
	if err := s.validateStatePaths(); err != nil {
		return InstallResult{}, "", fmt.Errorf("apply install: unsafe state path: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	projectLock, err := AcquireProjectLock(lockCtx, s.Base)
	if err != nil {
		return InstallResult{}, "", fmt.Errorf("apply install: project lock: %w", err)
	}
	defer projectLock.Release()
	if err := s.recoverTransaction(); err != nil {
		return InstallResult{}, "", fmt.Errorf("recover interrupted skill transaction: %w", err)
	}
	currentManifest, currentLock, err := s.reloadState(m, lock)
	if err != nil {
		return InstallResult{}, "", fmt.Errorf("apply install: %w", err)
	}
	if currentManifest != nil && AgentFromManifest(currentManifest) != s.Agent {
		return InstallResult{}, "", fmt.Errorf("project target changed since plan; create a new plan")
	}
	plan, err := s.planInstall(currentLock, candidate)
	if err != nil {
		return InstallResult{}, "", err
	}
	if expectedToken != plan.PlanToken {
		return InstallResult{}, "", fmt.Errorf("stale or missing plan token; re-plan before applying")
	}
	if plan.Decision == "unchanged" {
		if err := ctx.Err(); err != nil {
			return InstallResult{}, "", fmt.Errorf("apply install canceled: %w", err)
		}
		return InstallResult{ID: candidate.ID, Name: candidate.Name, Version: candidate.Version.String(), Target: plan.Target, Digest: plan.ContentDigest, Already: true, ProjectStateFingerprint: plan.StateFingerprint}, "unchanged", nil
	}
	if plan.Decision != "install" {
		return InstallResult{}, plan.Decision, fmt.Errorf("install blocked: %s", plan.Reason)
	}
	if err := ctx.Err(); err != nil {
		return InstallResult{}, "", fmt.Errorf("apply install canceled: %w", err)
	}
	result, err := s.installUnlockedContext(ctx, currentManifest, currentLock, candidate, plan.ContentDigest)
	if err != nil {
		return InstallResult{}, "failed", err
	}
	if lock != nil && currentLock != lock {
		*lock = *currentLock
	}
	if m != nil && currentManifest != nil && m != currentManifest {
		*m = *currentManifest
	}
	// Capture exact post-transaction state before releasing the project lock.
	// Callers may trust only this returned fingerprint when chaining applies.
	result.ProjectStateFingerprint, err = s.stateFingerprint()
	if err != nil {
		return InstallResult{}, "failed", fmt.Errorf("apply install: fingerprint post-state: %w", err)
	}
	return result, "installed", nil
}
