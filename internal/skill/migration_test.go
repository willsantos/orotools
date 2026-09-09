package skill_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/skill"
)

// legacyProject builds a project whose skills directory already contains an
// old bundled skill installed before the lock existed. The candidate tree is
// a single SKILL.md; legacyContent equal to that SKILL.md means "intact".
func legacyProject(t *testing.T, legacyContent string) (skill.Service, skill.Candidate, *skill.Lock, *manifest.Manifest) {
	t.Helper()
	svc, base := projectFixture(t)
	dest := filepath.Join(base, ".opencode", "skills", "code-review")
	if legacyContent != "" {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte(legacyContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cand := skill.Candidate{
		ID:        "recipe:dotnet/base/code-review",
		Name:      "code-review",
		Version:   mustSemver(t, "1.0.0"),
		Source:    skill.SourceRecipe,
		SourceRef: "dotnet",
		Path:      "base/code-review",
		Tree: skill.Tree{FS: fstest.MapFS{
			"skills/base/code-review/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("code-review", "Review guidelines", "1.0.0"))},
		}, Root: "skills/base/code-review"},
	}
	m, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return svc, cand, skill.NewLock(), m
}

func TestMigrateIntactBundledIsAdopted(t *testing.T) {
	svc, cand, lock, _ := legacyProject(t, skillDoc("code-review", "Review guidelines", "1.0.0"))
	targetBefore, err := os.ReadFile(filepath.Join(svc.Base, ".opencode", "skills", "code-review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := svc.MigrateBundled(lock, []skill.Candidate{cand})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State != skill.MigrationAdopted {
		t.Fatalf("results = %+v, want adopted", results)
	}
	// Lock persisted with the adopted entry.
	lockOnDisk, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatalf("lock not persisted: %v", err)
	}
	entry, ok := lockOnDisk.Lookup(cand.ID)
	if !ok || entry.Version != "1.0.0" || entry.Target != ".opencode/skills/code-review" {
		t.Errorf("entry = %+v", entry)
	}
	// Target untouched.
	targetAfter, _ := os.ReadFile(filepath.Join(svc.Base, ".opencode", "skills", "code-review", "SKILL.md"))
	if string(targetBefore) != string(targetAfter) {
		t.Error("adoption rewrote the target")
	}
	// Manifest untouched.
	mOnDisk, _ := manifest.Read(svc.ManifestPath)
	if mOnDisk.Version != 1 {
		t.Errorf("manifest promoted to v%d by adoption", mOnDisk.Version)
	}
	if len(mOnDisk.Skills.Managed) != 0 {
		t.Errorf("adoption added managed refs: %+v", mOnDisk.Skills.Managed)
	}
}

func TestMigrateMissingBundledIsNotRegistered(t *testing.T) {
	svc, cand, lock, _ := legacyProject(t, "")
	results, err := svc.MigrateBundled(lock, []skill.Candidate{cand})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State != skill.MigrationMissing {
		t.Fatalf("results = %+v, want missing", results)
	}
	if _, err := skill.ReadLock(svc.LockPath); err == nil {
		t.Error("lock created for missing target")
	}
}

func TestMigrateDivergentBundledIsNotAdopted(t *testing.T) {
	svc, cand, lock, _ := legacyProject(t, "---\nname: code-review\ndescription: customized locally\n---\n")
	results, err := svc.MigrateBundled(lock, []skill.Candidate{cand})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State != skill.MigrationDiverged {
		t.Fatalf("results = %+v, want diverged", results)
	}
	if _, err := skill.ReadLock(svc.LockPath); err == nil {
		t.Error("lock created for divergent target")
	}
}

func TestMigrateAlreadyManagedIsIdempotent(t *testing.T) {
	svc, cand, lock, _ := legacyProject(t, skillDoc("code-review", "Review guidelines", "1.0.0"))
	if _, err := svc.MigrateBundled(lock, []skill.Candidate{cand}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	results, err := svc.MigrateBundled(lock, []skill.Candidate{cand})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State != skill.MigrationManaged {
		t.Fatalf("results = %+v, want managed", results)
	}
	after, _ := os.ReadFile(svc.LockPath)
	if string(before) != string(after) {
		t.Error("idempotent migration rewrote the lock")
	}
}

func TestMigrateRejectsNonBundledCandidate(t *testing.T) {
	svc, _, lock, _ := legacyProject(t, "")
	cand := skill.Candidate{ID: "github:willsantos/skills_AI/x", Name: "x", Source: skill.SourceGitHub}
	if _, err := svc.MigrateBundled(lock, []skill.Candidate{cand}); err == nil {
		t.Fatal("expected error for non-bundled candidate")
	}
}

func TestAgentFromManifestDefaults(t *testing.T) {
	if got := skill.AgentFromManifest(nil); got != "opencode" {
		t.Errorf("nil manifest agent = %q", got)
	}
	if got := skill.AgentFromManifest(&manifest.Manifest{}); got != "opencode" {
		t.Errorf("legacy manifest agent = %q", got)
	}
	if got := skill.AgentFromManifest(&manifest.Manifest{AI: manifest.AIRef{Agent: "codex"}}); got != "codex" {
		t.Errorf("agent = %q", got)
	}
}

// External skills never become managed candidates; MigrateBundled over an
// empty candidate list is a no-op (FR-22).
func TestMigrateExternalOutOfScope(t *testing.T) {
	external := []recipe.ExternalSkill{{Installer: "impeccable"}}
	if skill.FlattenExternal(external)[0] != "impeccable" {
		t.Fatal("fixture broken")
	}
	var cands []skill.Candidate
	svc, _, lock, _ := legacyProject(t, "")
	results, err := svc.MigrateBundled(lock, cands)
	if err != nil || len(results) != 0 {
		t.Fatalf("empty migration should be a no-op: %+v, %v", results, err)
	}
}
