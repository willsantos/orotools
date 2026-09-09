package skill_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

// bundledWithVersion builds a bundled candidate whose SKILL.md carries the
// given version string.
func bundledWithVersion(t *testing.T, version string) skill.Candidate {
	t.Helper()
	root := "skills/base/code-review"
	doc := "---\nname: code-review\ndescription: Review guidelines\nmetadata:\n  version: \"" + version + "\"\n---\n# Code review v" + version + "\n"
	return skill.Candidate{
		ID:        "recipe:dotnet/base/code-review",
		Name:      "code-review",
		Version:   mustSemver(t, version),
		Source:    skill.SourceRecipe,
		SourceRef: "dotnet",
		Path:      "base/code-review",
		Tree: skill.Tree{FS: fstest.MapFS{
			root + "/SKILL.md": &fstest.MapFile{Data: []byte(doc)},
		}, Root: root},
	}
}

// installForUpdate installs the base bundled skill into a fresh project and
// returns the service and lock.
func installForUpdate(t *testing.T) (skill.Service, *skill.Lock) {
	t.Helper()
	svc, _ := projectFixture(t)
	lock := skill.NewLock()
	if _, err := svc.Install(nil, lock, bundledWithVersion(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	return svc, lock
}

// githubCandidate builds a managed GitHub candidate for fixtures.
func githubCandidate(t *testing.T, version, revision string) skill.Candidate {
	t.Helper()
	doc := "---\nname: local-pr-review\ndescription: Review PRs locally\nmetadata:\n  version: \"" + version + "\"\n---\n# local-pr-review " + revision + "\n"
	return skill.Candidate{
		ID:        "github:willsantos/skills_AI/local-pr-review",
		Name:      "local-pr-review",
		Version:   mustSemver(t, version),
		Source:    skill.SourceGitHub,
		SourceRef: "willsantos/skills_AI",
		Path:      "local-pr-review",
		Revision:  revision,
		Tree: skill.Tree{FS: fstest.MapFS{
			"local-pr-review/SKILL.md": &fstest.MapFile{Data: []byte(doc)},
		}, Root: "local-pr-review"},
	}
}

func TestUpdateCurrentWhenUpstreamIdentical(t *testing.T) {
	svc, lock := installForUpdate(t)
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.0.0")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 || report.Updated != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.Results[0].Decision != skill.UpdateCurrent {
		t.Errorf("decision = %s (%s)", report.Results[0].Decision, report.Results[0].Detail)
	}
}

func TestUpdateBundledBumpsVersion(t *testing.T) {
	svc, lock := installForUpdate(t)
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.1.0")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("report = %+v", report)
	}
	lockOnDisk, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := lockOnDisk.Lookup("recipe:dotnet/base/code-review")
	if entry.Version != "1.1.0" {
		t.Errorf("lock version = %s, want 1.1.0", entry.Version)
	}
	data, err := os.ReadFile(filepath.Join(svc.Base, ".opencode", "skills", "code-review", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "v1.1.0") {
		t.Errorf("target content not updated: %q, %v", data, err)
	}
	// No staging or backup leftovers.
	entries, _ := os.ReadDir(filepath.Join(svc.Base, ".opencode", "skills"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".orotools-") {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestUpdateDowngradeIgnored(t *testing.T) {
	svc, lock := installForUpdate(t)
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "0.9.0")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 || report.Results[0].Decision != skill.UpdateCurrent {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Results[0].Detail, "downgrade") {
		t.Errorf("detail = %q, want downgrade notice", report.Results[0].Detail)
	}
}

func TestUpdateGitHubBuildMetadataDoesNotForce(t *testing.T) {
	svc, _ := projectFixture(t)
	lock := skill.NewLock()
	m, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(m, lock, githubCandidate(t, "1.0.0", "rev1")); err != nil {
		t.Fatal(err)
	}
	// Upstream republished the same version with different build metadata and
	// content: not an update (FR-23) — and for GitHub the content is not
	// compared, so no packaging error applies.
	report, err := svc.Update(m, lock, skill.UpdateInput{Remote: []skill.Candidate{githubCandidate(t, "1.0.0+build.2", "rev2")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 || report.Updated != 0 {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Results[0].Detail, "build metadata") {
		t.Errorf("detail = %q", report.Results[0].Detail)
	}
}

func TestUpdateBundledChangedWithoutBumpFails(t *testing.T) {
	svc, lock := installForUpdate(t)
	cand := bundledWithVersion(t, "1.0.0")
	// Tamper with the source content while keeping the version.
	cand.Tree = skill.Tree{FS: fstest.MapFS{
		"skills/base/code-review/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: code-review\ndescription: rewritten\nmetadata:\n  version: \"1.0.0\"\n---\n# Rewritten\n")},
	}, Root: "skills/base/code-review"}
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{cand}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || report.Results[0].Decision != skill.UpdateFailed {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Results[0].Detail, "sem bump") {
		t.Errorf("detail = %q", report.Results[0].Detail)
	}
}

func TestUpdateDriftBlocksAndForceReplaces(t *testing.T) {
	svc, lock := installForUpdate(t)
	dest := filepath.Join(svc.Base, ".opencode", "skills", "code-review")
	if err := os.WriteFile(filepath.Join(dest, "local.txt"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundled := []skill.Candidate{bundledWithVersion(t, "1.1.0")}

	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || report.Results[0].Decision != skill.UpdateBlocked {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dest, "local.txt")); err != nil {
		t.Error("blocked update touched the destination")
	}

	report, err = svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("forced report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dest, "local.txt")); !os.IsNotExist(err) {
		t.Error("forced update kept the drifted file")
	}
}

func TestUpdateMissingFromCatalogKeepsDestination(t *testing.T) {
	svc, lock := installForUpdate(t)
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: nil}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(svc.Base, ".opencode", "skills", "code-review")); err != nil {
		t.Error("destination removed for missing upstream")
	}
}

func TestUpdateAgentMismatchBlocks(t *testing.T) {
	svc, lock := installForUpdate(t)
	// Change the agent: the derived target no longer matches the lock.
	svc.Agent = "cursor"
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.1.0")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || report.Results[0].Decision != skill.UpdateBlocked {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Results[0].Detail, "agent") {
		t.Errorf("detail = %q", report.Results[0].Detail)
	}
}

func TestUpdateDryRunMutatesNothing(t *testing.T) {
	svc, lock := installForUpdate(t)
	lockBefore, err := os.ReadFile(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.1.0")}}, skill.UpdateOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 || report.Results[0].To != "1.1.0" {
		t.Fatalf("report = %+v", report)
	}
	lockAfter, _ := os.ReadFile(svc.LockPath)
	if string(lockBefore) != string(lockAfter) {
		t.Error("dry-run rewrote the lock")
	}
	data, _ := os.ReadFile(filepath.Join(svc.Base, ".opencode", "skills", "code-review", "SKILL.md"))
	if strings.Contains(string(data), "v1.1.0") {
		t.Error("dry-run rewrote the destination")
	}
}

func TestUpdatePartialBatchBestEffort(t *testing.T) {
	svc, base := projectFixture(t)
	lock := skill.NewLock()
	m, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	first := bundledWithVersion(t, "1.0.0")
	if _, err := svc.Install(m, lock, first); err != nil {
		t.Fatal(err)
	}
	second := skill.Candidate{
		ID:        "github:willsantos/skills_AI/local-pr-review",
		Name:      "local-pr-review",
		Version:   mustSemver(t, "1.0.0"),
		Source:    skill.SourceGitHub,
		SourceRef: "willsantos/skills_AI",
		Path:      "local-pr-review",
		Revision:  "aaa",
		Tree: skill.Tree{FS: fstest.MapFS{
			"local-pr-review/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: local-pr-review\ndescription: d\nmetadata:\n  version: \"1.0.0\"\n---\n")},
		}, Root: "local-pr-review"},
	}
	if _, err := svc.Install(m, lock, second); err != nil {
		t.Fatal(err)
	}
	// Upstream: first moves to 1.1.0; second vanished from the catalog.
	report, err := svc.Update(m, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.1.0")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 || report.Failed != 1 {
		t.Fatalf("report = %+v", report)
	}
	// The successful update was persisted despite the failed sibling.
	lockOnDisk, _ := skill.ReadLock(svc.LockPath)
	entry, _ := lockOnDisk.Lookup(first.ID)
	if entry.Version != "1.1.0" {
		t.Errorf("first version = %s", entry.Version)
	}
	if _, err := os.Stat(filepath.Join(base, ".opencode", "skills", "local-pr-review")); err != nil {
		t.Error("failed sibling removed the destination")
	}
}

func TestUpdateForceRegistersDivergedBundled(t *testing.T) {
	svc, _, lock, _ := legacyProject(t, "---\nname: code-review\ndescription: customized\nmetadata:\n  version: \"1.0.0\"\n---\n")
	migration, err := svc.MigrateBundled(lock, []skill.Candidate{bundledWithVersion(t, "1.1.0")})
	if err != nil {
		t.Fatal(err)
	}
	if migration[0].State != skill.MigrationDiverged {
		t.Fatalf("migration = %+v", migration)
	}
	// Without --force: blocked.
	report, err := svc.Update(nil, lock, skill.UpdateInput{
		Bundled:   []skill.Candidate{bundledWithVersion(t, "1.1.0")},
		Migration: migration,
	}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 {
		t.Fatalf("report = %+v", report)
	}
	// With --force: replaced and registered.
	report, err = svc.Update(nil, lock, skill.UpdateInput{
		Bundled:   []skill.Candidate{bundledWithVersion(t, "1.1.0")},
		Migration: migration,
	}, skill.UpdateOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("forced report = %+v", report)
	}
	lockOnDisk, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lockOnDisk.Lookup("recipe:dotnet/base/code-review"); !ok {
		t.Error("diverged skill not registered after forced replace")
	}
	data, _ := os.ReadFile(filepath.Join(svc.Base, ".opencode", "skills", "code-review", "SKILL.md"))
	if strings.Contains(string(data), "customized") {
		t.Error("local customization survived forced replace")
	}
}

func TestUpdateIdempotentRerun(t *testing.T) {
	svc, lock := installForUpdate(t)
	in := skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.1.0")}}
	if _, err := svc.Update(nil, lock, in, skill.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	report, err := svc.Update(nil, lock, in, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 0 || report.Current != 1 {
		t.Fatalf("second run report = %+v", report)
	}
}

// Regression (review 2026-09-09): drift must be detected even without an
// upstream bump — a modified "current" skill is blocked, never reported as
// current (FR-19/FR-25).
func TestUpdateDriftWithoutBumpBlocksAndForceRestores(t *testing.T) {
	svc, lock := installForUpdate(t)
	dest := filepath.Join(svc.Base, ".opencode", "skills", "code-review")
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("modificado localmente"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundled := []skill.Candidate{bundledWithVersion(t, "1.0.0")}

	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || report.Results[0].Decision != skill.UpdateBlocked {
		t.Fatalf("report = %+v, want blocked", report)
	}

	// --force restores the authoritative content even at the same version.
	report, err = svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 || report.Results[0].Decision != skill.UpdateAvailable {
		t.Fatalf("forced report = %+v", report)
	}
	data, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if !strings.Contains(string(data), "v1.0.0") {
		t.Errorf("content not restored: %q", data)
	}
	// After restoration the skill is current again.
	report, err = svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 {
		t.Fatalf("post-restore report = %+v", report)
	}
}

func TestUpdateMissingTargetBlocksAndForceReinstalls(t *testing.T) {
	svc, lock := installForUpdate(t)
	dest := filepath.Join(svc.Base, ".opencode", "skills", "code-review")
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}
	bundled := []skill.Candidate{bundledWithVersion(t, "1.0.0")}

	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || !strings.Contains(report.Results[0].Detail, "ausente") {
		t.Fatalf("report = %+v, want missing-target blocked", report)
	}

	report, err = svc.Update(nil, lock, skill.UpdateInput{Bundled: bundled}, skill.UpdateOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("forced report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("skill not reinstalled: %v", err)
	}
}

// Regression (review 2026-09-09, round 2): a published downgrade is never
// installed — not even with --force, which authorizes discarding drift, not
// downgrading (FR-23/FR-24).
func TestUpdateDowngradeNeverInstalledEvenWithForce(t *testing.T) {
	svc, lock := installForUpdate(t)
	dest := filepath.Join(svc.Base, ".opencode", "skills", "code-review")
	downstream := []skill.Candidate{bundledWithVersion(t, "0.9.0")}

	// No drift: downgrade is ignored, the skill stays current.
	report, err := svc.Update(nil, lock, skill.UpdateInput{Bundled: downstream}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 1 || !strings.Contains(report.Results[0].Detail, "downgrade ignorado") {
		t.Fatalf("report = %+v, want current with downgrade notice", report)
	}

	// Drift + --force: still no downgrade; the item is blocked with the
	// combined reason and nothing is touched.
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("modificado"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = svc.Update(nil, lock, skill.UpdateInput{Bundled: downstream}, skill.UpdateOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || !strings.Contains(report.Results[0].Detail, "downgrade não é instalado") {
		t.Fatalf("report = %+v, want blocked with downgrade reason", report)
	}
	data, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if string(data) != "modificado" {
		t.Errorf("forced downgrade rewrote the target: %q", data)
	}
	lockOnDisk, _ := skill.ReadLock(svc.LockPath)
	entry, _ := lockOnDisk.Lookup("recipe:dotnet/base/code-review")
	if entry.Version != "1.0.0" {
		t.Errorf("lock version = %s, want unchanged 1.0.0", entry.Version)
	}

	// Missing target + downstream only: --force cannot reinstall a downgrade.
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}
	report, err = svc.Update(nil, lock, skill.UpdateInput{Bundled: downstream}, skill.UpdateOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || !strings.Contains(report.Results[0].Detail, "downgrade não é instalado") {
		t.Fatalf("report = %+v, want blocked", report)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("downgraded skill was installed over a missing target")
	}
}

// Regression (review 2026-09-09, round 2): a failed persist during update
// must restore the exact previous bytes — the backup ownership marker is
// bookkeeping and must never reach the restored target (FR-29/NFR-4).
func TestUpdatePersistFailureRestoresExactBytes(t *testing.T) {
	svc, lock := installForUpdate(t)
	destRel := ".opencode/skills/code-review"
	destDir := filepath.Join(svc.Base, ".opencode", "skills", "code-review")
	before, err := os.ReadFile(filepath.Join(destDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := lock.Lookup("recipe:dotnet/base/code-review")
	digestBefore, err := skill.Digest(os.DirFS(svc.Base), destRel)
	if err != nil {
		t.Fatal(err)
	}
	if digestBefore != entry.ContentSHA256 {
		t.Fatalf("fixture drift: %q vs %q", digestBefore, entry.ContentSHA256)
	}
	lockBefore, err := os.ReadFile(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}

	// Make the lock save fail on the next update.
	broken := svc
	broken.LockPath = filepath.Join(svc.Base, skill.LockDir, "sub", skill.LockFilename)
	if err := os.MkdirAll(filepath.Join(svc.Base, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.Base, skill.LockDir, "sub"), []byte("blocks dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := broken.Update(nil, lock, skill.UpdateInput{Bundled: []skill.Candidate{bundledWithVersion(t, "1.1.0")}}, skill.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 {
		t.Fatalf("report = %+v, want 1 failure", report)
	}

	// Restored tree is byte-identical: content, no marker, digest intact.
	after, _ := os.ReadFile(filepath.Join(destDir, "SKILL.md"))
	if !bytes.Equal(before, after) {
		t.Error("restored target differs from previous bytes")
	}
	if _, err := os.Stat(filepath.Join(destDir, ".orotools-owner.json")); !os.IsNotExist(err) {
		t.Error("ownership marker left in the restored target")
	}
	digestAfter, err := skill.Digest(os.DirFS(svc.Base), destRel)
	if err != nil {
		t.Fatal(err)
	}
	if digestAfter != entry.ContentSHA256 {
		t.Errorf("digest mismatch after rollback: %q vs %q", digestAfter, entry.ContentSHA256)
	}
	lockAfter, _ := os.ReadFile(svc.LockPath)
	if !bytes.Equal(lockBefore, lockAfter) {
		t.Error("lock changed despite persist failure")
	}
	entries, _ := os.ReadDir(filepath.Join(svc.Base, ".opencode", "skills"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".orotools-") {
			t.Errorf("orphan left behind: %s", e.Name())
		}
	}
}
