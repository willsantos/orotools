package skill_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

func TestInstallPlanIsReadOnlyAndApplyRequiresFreshToken(t *testing.T) {
	svc, base := projectFixture(t)
	candidate := recipeCandidate(t, candidateTree())
	lock := skill.NewLock()
	before, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanInstall(lock, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Decision != "install" || plan.DigestAlgorithm != skill.DigestAlgorithm || plan.PlanToken == "" {
		t.Fatalf("plan = %+v", plan)
	}
	after, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("plan mutated project root: before=%v after=%v", before, after)
	}
	if _, _, err := svc.ApplyPlannedInstall(nil, lock, candidate, "sha256:stale"); err == nil {
		t.Fatal("stale token accepted")
	}
	if _, err := os.Stat(filepath.Join(base, ".opencode", "skills", candidate.Name)); !os.IsNotExist(err) {
		t.Fatalf("stale token changed destination: %v", err)
	}
	project, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	result, decision, err := svc.ApplyPlannedInstall(project, lock, candidate, plan.PlanToken)
	if err != nil || decision != "installed" || result.Digest != plan.ContentDigest {
		t.Fatalf("ApplyPlannedInstall() = %+v, %q, %v", result, decision, err)
	}
	postState, err := svc.ProjectStateFingerprint()
	if err != nil || result.ProjectStateFingerprint == "" || result.ProjectStateFingerprint != postState {
		t.Fatalf("apply returned post-state fingerprint %q, actual %q, err %v", result.ProjectStateFingerprint, postState, err)
	}
	unchangedPlan, err := svc.PlanInstall(lock, candidate)
	if err != nil || unchangedPlan.Decision != "unchanged" {
		t.Fatalf("unchanged plan = %+v, %v", unchangedPlan, err)
	}
	unchanged, decision, err := svc.ApplyPlannedInstall(project, lock, candidate, unchangedPlan.PlanToken)
	if err != nil || decision != "unchanged" || unchanged.ProjectStateFingerprint != unchangedPlan.StateFingerprint {
		t.Fatalf("unchanged apply fingerprint = %+v, %q, %v; want %q", unchanged, decision, err, unchangedPlan.StateFingerprint)
	}
	entry, ok := lock.LookupTarget(candidate.ID, plan.Target)
	if !ok || entry.DigestAlgorithm != skill.DigestAlgorithm {
		t.Fatalf("lock entry lacks named digest algorithm: %+v", entry)
	}
}

func TestApplyUnchangedStillBlocksOnPendingJournal(t *testing.T) {
	svc, base := projectFixture(t)
	candidate := recipeCandidate(t, candidateTree())
	lock := skill.NewLock()
	if _, err := svc.Install(nil, lock, candidate); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanInstall(lock, candidate)
	if err != nil || plan.Decision != "unchanged" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	journalPath := filepath.Join(base, skill.LockDir, ".skills-transaction.json")
	if err := os.WriteFile(journalPath, []byte(`{"arbitrary":"journal"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyPlannedInstall(nil, lock, candidate, plan.PlanToken); err == nil {
		t.Fatal("unchanged apply bypassed pending-journal check")
	}
	if got, err := os.ReadFile(journalPath); err != nil || string(got) != `{"arbitrary":"journal"}` {
		t.Fatalf("pending journal changed: %q %v", got, err)
	}
}

func TestInstallPlanBlocksUnownedDestinationAndDrift(t *testing.T) {
	svc, base := projectFixture(t)
	candidate := recipeCandidate(t, candidateTree())
	lock := skill.NewLock()
	target := filepath.Join(base, ".opencode", "skills", candidate.Name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("user data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanInstall(lock, candidate)
	if err != nil || plan.Decision != "blocked" {
		t.Fatalf("unowned destination plan = %+v, %v", plan, err)
	}
	if got, _ := os.ReadFile(filepath.Join(target, "SKILL.md")); string(got) != "user data\n" {
		t.Fatalf("unowned destination changed: %q", got)
	}
	if _, _, err := svc.ApplyPlannedInstall(nil, lock, candidate, plan.PlanToken); err == nil {
		t.Fatal("apply accepted blocked plan")
	}
}

func TestApplyRejectsDriftAfterPlan(t *testing.T) {
	svc, base := projectFixture(t)
	candidate := recipeCandidate(t, candidateTree())
	lock := skill.NewLock()
	plan, err := svc.PlanInstall(lock, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, ".opencode", "skills", candidate.Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".opencode", "skills", candidate.Name, "SKILL.md"), []byte("raced write"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyPlannedInstall(nil, lock, candidate, plan.PlanToken); err == nil {
		t.Fatal("apply accepted destination changed after plan")
	}
	got, _ := os.ReadFile(filepath.Join(base, ".opencode", "skills", candidate.Name, "SKILL.md"))
	if string(got) != "raced write" {
		t.Fatalf("concurrent destination was overwritten: %q", got)
	}
}

func TestProjectLockSerializesAndRequiresOwnerToken(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := skill.AcquireProjectLock(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer waitCancel()
	if _, err := skill.AcquireProjectLock(waitCtx, root); !errors.Is(err, skill.ErrProjectLocked) {
		t.Fatalf("second acquire error = %v", err)
	}
	ownerPath := filepath.Join(root, skill.LockDir, ".skills-operation.lock", "owner.json")
	if err := os.WriteFile(ownerPath, []byte(`{"token":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err == nil {
		t.Fatal("release removed a lock owned by another token")
	}
	if _, err := os.Stat(filepath.Dir(ownerPath)); err != nil {
		t.Fatalf("foreign lock was removed: %v", err)
	}
}

func TestAcquireProjectLockRejectsAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lock, err := skill.AcquireProjectLock(ctx, t.TempDir()); err == nil {
		_ = lock.Release()
		t.Fatal("canceled context acquired project lock")
	}
}

func TestSafeProjectPathRejectsSymlinkedParent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".opencode")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := skill.SafeProjectPath(root, ".opencode/skills/x"); err == nil {
		t.Fatal("symlinked project path accepted")
	}
}

func TestInstallRejectsReservedOwnerMarkerInSource(t *testing.T) {
	svc, _ := projectFixture(t)
	candidate := recipeCandidate(t, fstest.MapFS{
		"skill/SKILL.md":             &fstest.MapFile{Data: []byte(skillDoc("code-review", "Review", "1.0.0"))},
		"skill/.orotools-owner.json": &fstest.MapFile{Data: []byte(`{"skill":"forged"}`)},
	})
	if _, err := svc.Install(&manifest.Manifest{Version: 1}, skill.NewLock(), candidate); err == nil {
		t.Fatal("source with reserved marker was installed")
	}
	if _, err := os.Stat(filepath.Join(svc.Base, ".opencode", "skills", candidate.Name)); !os.IsNotExist(err) {
		t.Fatalf("reserved-marker source mutated destination: %v", err)
	}
}

func TestInstallReloadsLockUnderWriterLock(t *testing.T) {
	svc, _ := projectFixture(t)
	staleLock := skill.NewLock()
	manifestOne, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	first := recipeCandidate(t, candidateTree())
	second := first
	second.ID, second.Name = "recipe:dotnet/base/another", "another-skill"
	if _, err := svc.Install(manifestOne, staleLock, first); err != nil {
		t.Fatal(err)
	}
	manifestTwo, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(manifestTwo, staleLock, second); err != nil {
		t.Fatal(err)
	}
	loaded, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Skills) != 2 {
		t.Fatalf("stale writer overwrote other entry: %+v", loaded.Skills)
	}
}

func TestApplyRejectsAgentChangeWhileWaitingForLock(t *testing.T) {
	svc, base := projectFixture(t)
	candidate := recipeCandidate(t, candidateTree())
	lock := skill.NewLock()
	plan, err := svc.PlanInstall(lock, candidate)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	held, err := skill.AcquireProjectLock(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct{ err error }
	done := make(chan outcome, 1)
	go func() {
		_, _, applyErr := svc.ApplyPlannedInstallContext(ctx, nil, lock, candidate, plan.PlanToken)
		done <- outcome{applyErr}
	}()
	time.Sleep(60 * time.Millisecond)
	changed := &manifest.Manifest{Version: 1, Project: manifest.ProjectRef{Name: "demo"}, AI: manifest.AIRef{Agent: "cursor"}}
	if err := manifest.Write(svc.ManifestPath, changed); err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err == nil {
			t.Fatal("apply accepted target change while waiting for writer lock")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("apply did not finish")
	}
	if _, err := os.Stat(filepath.Join(base, ".opencode", "skills", candidate.Name)); !os.IsNotExist(err) {
		t.Fatalf("stale-target apply mutated project: %v", err)
	}
}

func TestPendingTransactionBlocksWithoutChangingSubsequentEdits(t *testing.T) {
	svc, base := projectFixture(t)
	candidate := recipeCandidate(t, candidateTree())
	if _, err := svc.Install(&manifest.Manifest{Version: 1, Project: manifest.ProjectRef{Name: "demo"}}, skill.NewLock(), candidate); err != nil {
		t.Fatal(err)
	}
	targetRel := filepath.ToSlash(filepath.Join(".opencode", "skills", candidate.Name))
	target := filepath.Join(base, filepath.FromSlash(targetRel))
	oldManifest, err := os.ReadFile(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	oldLock, err := os.ReadFile(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(target), ".orotools-backup-recovery-test")
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("new incomplete target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(svc.ManifestPath, &manifest.Manifest{Version: 2, Project: manifest.ProjectRef{Name: "wrong"}}); err != nil {
		t.Fatal(err)
	}
	if err := skill.NewLock().Save(svc.LockPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(outside, []byte("outside sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := map[string]any{"target": "../../valuable", "backup": backup, "stage": outside, "hadManifest": true, "oldManifest": base64.StdEncoding.EncodeToString(oldManifest), "hadLock": true, "oldLock": base64.StdEncoding.EncodeToString(oldLock)}
	data, _ := json.Marshal(journal)
	if err := os.WriteFile(filepath.Join(base, skill.LockDir, ".skills-transaction.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.CleanOrphans()
	recovered, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(recovered) != "new incomplete target" {
		t.Fatalf("cleanup mutated target with pending journal: %q", recovered)
	}
	manifestAfter, err := os.ReadFile(svc.ManifestPath)
	if err != nil || string(manifestAfter) == string(oldManifest) {
		t.Fatalf("cleanup restored stale manifest over later edit: %v", err)
	}
	lockAfter, err := os.ReadFile(svc.LockPath)
	if err != nil || string(lockAfter) == string(oldLock) {
		t.Fatalf("cleanup restored stale lock over later edit: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside sentinel" {
		t.Fatalf("arbitrary journal stage path mutated: %q %v", got, err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup was touched: %v", err)
	}
	if _, err := svc.Install(nil, skill.NewLock(), candidate); err == nil {
		t.Fatal("writer proceeded with pending transaction")
	}
	if got, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(got) != "new incomplete target" {
		t.Fatalf("blocked writer modified target: %q %v", got, err)
	}
	if _, err := os.ReadFile(filepath.Join(base, skill.LockDir, ".skills-transaction.json")); err != nil {
		t.Fatalf("journal evidence removed: %v", err)
	}
}

func TestRemovedManifestAndLockInvalidatePlanAndDoNotResurrectWriterSnapshot(t *testing.T) {
	svc, base := projectFixture(t)
	first := recipeCandidate(t, candidateTree())
	staleManifest, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	staleLock := skill.NewLock()
	if _, err := svc.Install(staleManifest, staleLock, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.Name = "recipe:dotnet/base/second", "second-skill"
	plan, err := svc.PlanInstall(staleLock, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(svc.ManifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(svc.LockPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ApplyPlannedInstall(staleManifest, staleLock, second, plan.PlanToken); err == nil {
		t.Fatal("apply accepted removed state files")
	}
	if _, err := os.Stat(filepath.Join(base, ".opencode", "skills", second.Name)); !os.IsNotExist(err) {
		t.Fatalf("stale apply mutated project: %v", err)
	}
	if _, err := svc.Install(staleManifest, staleLock, second); err != nil {
		t.Fatal(err)
	}
	loaded, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Skills) != 1 || loaded.Skills[0].ID != second.ID {
		t.Fatalf("writer resurrected removed lock snapshot: %+v", loaded.Skills)
	}
	if _, err := os.Stat(svc.ManifestPath); !os.IsNotExist(err) {
		t.Fatalf("writer resurrected removed manifest: %v", err)
	}
}
