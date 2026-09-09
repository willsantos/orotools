package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

// projectFixture creates a temp project with a manifest and returns a service
// wired for the opencode agent.
func projectFixture(t *testing.T) (skill.Service, string) {
	t.Helper()
	base := t.TempDir()
	mPath := filepath.Join(base, manifest.Filename)
	if err := manifest.Write(mPath, &manifest.Manifest{
		Version: 1,
		Project: manifest.ProjectRef{Name: "demo"},
		AI:      manifest.AIRef{Agent: "opencode"},
	}); err != nil {
		t.Fatal(err)
	}
	svc := skill.Service{
		Base:         base,
		Agent:        "opencode",
		ManifestPath: mPath,
		LockPath:     filepath.Join(base, skill.LockDir, skill.LockFilename),
	}
	return svc, base
}

func recipeCandidate(t *testing.T, fs fsLike) skill.Candidate {
	t.Helper()
	return skill.Candidate{
		ID:          "recipe:dotnet/base/code-review",
		Name:        "code-review",
		Description: "Review guidelines",
		Version:     mustSemver(t, "1.0.0"),
		Source:      skill.SourceRecipe,
		SourceRef:   "dotnet",
		Path:        "base/code-review",
		Tree:        skill.Tree{FS: fs, Root: "skills/base/code-review"},
	}
}

// fsLike avoids importing io/fs just for the fixture signature.
type fsLike = fstest.MapFS

func candidateTree() fstest.MapFS {
	return fstest.MapFS{
		"skills/base/code-review/SKILL.md":            &fstest.MapFile{Data: []byte(skillDoc("code-review", "Review guidelines", "1.0.0"))},
		"skills/base/code-review/references/intro.md": &fstest.MapFile{Data: []byte("# intro\n")},
		"skills/base/code-review/scripts/run.sh":      &fstest.MapFile{Data: []byte("#!/bin/sh\n")},
	}
}

func TestServiceInstallBundled(t *testing.T) {
	svc, base := projectFixture(t)
	m, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lock := skill.NewLock()

	res, err := svc.Install(m, lock, recipeCandidate(t, candidateTree()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Already {
		t.Fatal("first install reported Already")
	}
	if res.Target != ".opencode/skills/code-review" {
		t.Errorf("Target = %q", res.Target)
	}
	// Full tree installed, nested directories included.
	data, err := os.ReadFile(filepath.Join(base, ".opencode", "skills", "code-review", "references", "intro.md"))
	if err != nil {
		t.Fatalf("nested file missing: %v", err)
	}
	if string(data) != "# intro\n" {
		t.Errorf("content = %q", data)
	}
	// Digest matches the installed tree.
	d, err := skill.Digest(os.DirFS(base), res.Target)
	if err != nil {
		t.Fatal(err)
	}
	if d != res.Digest {
		t.Errorf("Digest = %q, want %q", d, res.Digest)
	}
	// Manifest stays v1 (no managed ref for bundled).
	mOnDisk, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if mOnDisk.Version != 1 || len(mOnDisk.Skills.Managed) != 0 {
		t.Errorf("manifest changed by bundled install: v%d managed=%v", mOnDisk.Version, mOnDisk.Skills.Managed)
	}
	// Lock persisted with the entry.
	lockOnDisk, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lockOnDisk.Lookup(res.ID); !ok {
		t.Errorf("lock missing entry %q: %+v", res.ID, lockOnDisk.Skills)
	}
}

func TestServiceInstallExecBitPreserved(t *testing.T) {
	svc, base := projectFixture(t)
	// fstest.MapFS cannot express executable modes; use a real directory.
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "skills", "base", "code-review", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(src, "skills", "base", "code-review", "scripts", "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "skills", "base", "code-review", "SKILL.md"), []byte(skillDoc("code-review", "d", "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	cand := skill.Candidate{
		ID:        "recipe:dotnet/base/code-review",
		Name:      "code-review",
		Version:   mustSemver(t, "1.0.0"),
		Source:    skill.SourceRecipe,
		SourceRef: "dotnet",
		Path:      "base/code-review",
		Tree:      skill.Tree{FS: os.DirFS(src), Root: "skills/base/code-review"},
	}
	m, _ := manifest.Read(svc.ManifestPath)
	lock := skill.NewLock()
	if _, err := svc.Install(m, lock, cand); err != nil {
		t.Fatal(err)
	}
	installed, err := os.Stat(filepath.Join(base, ".opencode", "skills", "code-review", "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if installed.Mode()&0o111 == 0 {
		t.Errorf("executable bit lost: %v", installed.Mode())
	}
	docs, err := os.Stat(filepath.Join(base, ".opencode", "skills", "code-review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if docs.Mode()&0o111 != 0 {
		t.Errorf("regular file became executable: %v", docs.Mode())
	}
}

func TestServiceInstallGitHubAddsManaged(t *testing.T) {
	svc, _ := projectFixture(t)
	m, _ := manifest.Read(svc.ManifestPath)
	lock := skill.NewLock()
	cand := skill.Candidate{
		ID:        "github:willsantos/skills_AI/local-pr-review",
		Name:      "local-pr-review",
		Version:   mustSemver(t, "1.0.0"),
		Source:    skill.SourceGitHub,
		SourceRef: "willsantos/skills_AI",
		Path:      "local-pr-review",
		Revision:  "abc123",
		Tree: skill.Tree{FS: fstest.MapFS{
			"local-pr-review/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("local-pr-review", "d", "1.0.0"))},
		}, Root: "local-pr-review"},
	}
	if _, err := svc.Install(m, lock, cand); err != nil {
		t.Fatal(err)
	}
	mOnDisk, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if mOnDisk.Version != 2 {
		t.Errorf("version = %d, want 2 after managed install", mOnDisk.Version)
	}
	want := manifest.ManagedSkillRef{Source: "github:willsantos/skills_AI", Name: "local-pr-review"}
	if len(mOnDisk.Skills.Managed) != 1 || mOnDisk.Skills.Managed[0] != want {
		t.Errorf("Managed = %+v, want [%+v]", mOnDisk.Skills.Managed, want)
	}
	lockOnDisk, _ := skill.ReadLock(svc.LockPath)
	entry, ok := lockOnDisk.Lookup(cand.ID)
	if !ok || entry.Revision != "abc123" {
		t.Errorf("lock entry = %+v", entry)
	}
}

func TestServiceInstallConflictCreateOnly(t *testing.T) {
	svc, base := projectFixture(t)
	dest := filepath.Join(base, ".opencode", "skills", "code-review")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "custom.txt"), []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Read(svc.ManifestPath)
	lock := skill.NewLock()
	if _, err := svc.Install(m, lock, recipeCandidate(t, candidateTree())); err == nil {
		t.Fatal("expected conflict for existing unmanaged destination")
	} else if !strings.Contains(err.Error(), "custom.txt") && !strings.Contains(err.Error(), "já existe") {
		t.Logf("conflict error: %v", err)
	}
	// User content untouched.
	b, err := os.ReadFile(filepath.Join(dest, "custom.txt"))
	if err != nil || string(b) != "user data" {
		t.Errorf("user content clobbered: %q, %v", b, err)
	}
	// No lock file created, manifest unchanged.
	if _, err := skill.ReadLock(svc.LockPath); err == nil {
		t.Error("lock persisted despite conflict")
	}
}

func TestServiceInstallIdempotent(t *testing.T) {
	svc, _ := projectFixture(t)
	m, _ := manifest.Read(svc.ManifestPath)
	lock := skill.NewLock()
	if _, err := svc.Install(m, lock, recipeCandidate(t, candidateTree())); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Install(m, lock, recipeCandidate(t, candidateTree()))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Already {
		t.Errorf("re-install: Already = false, want true (%+v)", res)
	}
	lockAfter, _ := os.ReadFile(svc.LockPath)
	if string(lockBefore) != string(lockAfter) {
		t.Error("idempotent re-install changed the lock")
	}
}

func TestServiceInstallRollsBackOnPersistFailure(t *testing.T) {
	svc, base := projectFixture(t)
	// Point LockPath at a path whose parent is a regular file, making the
	// lock directory creation fail.
	svc.LockPath = filepath.Join(base, skill.LockDir, "sub", skill.LockFilename)
	if err := os.MkdirAll(filepath.Join(base, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, skill.LockDir, "sub"), []byte("file blocks dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Read(svc.ManifestPath)
	manifestBefore, _ := os.ReadFile(svc.ManifestPath)
	lock := skill.NewLock()
	if _, err := svc.Install(m, lock, recipeCandidate(t, candidateTree())); err == nil {
		t.Fatal("expected persist failure")
	}
	// Swap rolled back: target gone, staging cleaned.
	if _, err := os.Stat(filepath.Join(base, ".opencode", "skills", "code-review")); !os.IsNotExist(err) {
		t.Error("target not rolled back after persist failure")
	}
	entries, _ := os.ReadDir(filepath.Join(base, ".opencode", "skills"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".orotools-") {
			t.Errorf("orphan left behind: %s", e.Name())
		}
	}
	// Manifest untouched.
	manifestAfter, _ := os.ReadFile(svc.ManifestPath)
	if string(manifestBefore) != string(manifestAfter) {
		t.Error("manifest modified despite persist failure")
	}
}

func TestServiceCleanOrphans(t *testing.T) {
	svc, base := projectFixture(t)
	skillsDir := filepath.Join(base, ".opencode", "skills")
	markerJSON := `{"skill":"recipe:x/y"}` + "\n"
	for _, dir := range []string{".orotools-stage-code-review-x", ".orotools-backup-code-review-y"} {
		if err := os.MkdirAll(filepath.Join(skillsDir, dir, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillsDir, dir, ".orotools-owner.json"), []byte(markerJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Prefix alone is not ownership: without a verifiable marker, nothing is
	// touched (review 2026-09-09, blocker 2).
	if err := os.MkdirAll(filepath.Join(skillsDir, ".orotools-stage-user-data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, ".orotools-stage-user-data", "keep.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, ".orotools-backup-gibberish"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, ".orotools-backup-gibberish", ".orotools-owner.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "real-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc.CleanOrphans()

	for _, orphan := range []string{".orotools-stage-code-review-x", ".orotools-backup-code-review-y"} {
		if _, err := os.Stat(filepath.Join(skillsDir, orphan)); !os.IsNotExist(err) {
			t.Errorf("marked orphan %q not removed", orphan)
		}
	}
	for _, kept := range []string{".orotools-stage-user-data", ".orotools-backup-gibberish", "real-skill"} {
		if _, err := os.Stat(filepath.Join(skillsDir, kept)); err != nil {
			t.Errorf("directory without verifiable ownership was removed: %s", kept)
		}
	}
	keep, err := os.ReadFile(filepath.Join(skillsDir, ".orotools-stage-user-data", "keep.txt"))
	if err != nil || string(keep) != "mine" {
		t.Errorf("unmarked directory contents touched: %q, %v", keep, err)
	}
}

// Regression (review 2026-09-09): remote frontmatter `name` is untrusted and
// must never escape the agent skills directory (FR-13).
func TestTargetForRejectsUnsafeNames(t *testing.T) {
	svc := skill.Service{Agent: "opencode"}
	for _, name := range []string{"", "../evil", "..\\evil", "sub/dir", "sub\\dir", ".", "..", "a/../b", ".orotools-stage-x", ".orotools-backup-y"} {
		if _, err := svc.TargetFor(name); err == nil {
			t.Errorf("TargetFor(%q) accepted unsafe name", name)
		}
	}
	if got, err := svc.TargetFor("code-review"); err != nil || got != ".opencode/skills/code-review" {
		t.Errorf("TargetFor(safe) = %q, %v", got, err)
	}
}

func TestInstallUnsafeNameFailsWithoutSideEffects(t *testing.T) {
	svc, base := projectFixture(t)
	m, _ := manifest.Read(svc.ManifestPath)
	lock := skill.NewLock()
	cand := skill.Candidate{
		ID:        "github:willsantos/skills_AI/../evil",
		Name:      "../evil",
		Version:   mustSemver(t, "1.0.0"),
		Source:    skill.SourceGitHub,
		SourceRef: "willsantos/skills_AI",
		Path:      "evil",
		Tree: skill.Tree{FS: fstest.MapFS{
			"evil/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("evil", "d", "1.0.0"))},
		}, Root: "evil"},
	}
	if _, err := svc.Install(m, lock, cand); err == nil {
		t.Fatal("expected error for traversal name")
	}
	// Nothing created anywhere: no skills dir, no lock, no manifest promotion.
	if _, err := os.Stat(filepath.Join(base, ".opencode")); !os.IsNotExist(err) {
		t.Error("skills directory created despite unsafe name")
	}
	if _, err := skill.ReadLock(svc.LockPath); err == nil {
		t.Error("lock persisted despite unsafe name")
	}
	mOnDisk, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if mOnDisk.Version != 1 || len(mOnDisk.Skills.Managed) != 0 {
		t.Errorf("managed ref persisted despite unsafe name: v%d %+v", mOnDisk.Version, mOnDisk.Skills.Managed)
	}
}
