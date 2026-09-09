package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Masterminds/semver/v3"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

// --- fakes ---

type fakeSelector struct {
	ids []string
	err error

	gotTitle   string
	gotOptions []skillOption
}

func (f *fakeSelector) Select(title string, options []skillOption) ([]string, error) {
	f.gotTitle = title
	f.gotOptions = options
	if f.err != nil {
		return nil, f.err
	}
	return f.ids, nil
}

type fakeCatalog struct{ cands []skill.Candidate }

func (f fakeCatalog) List(context.Context) ([]skill.Candidate, []skill.Warning, error) {
	return f.cands, nil, nil
}

// --- fixtures ---

func skillsProject(t *testing.T) (skill.Service, string) {
	t.Helper()
	base := t.TempDir()
	mPath := filepath.Join(base, manifest.Filename)
	if err := manifest.Write(mPath, &manifest.Manifest{
		Version: 1,
		Recipe:  manifest.RecipeRef{Name: "dotnet", Version: 1},
		Project: manifest.ProjectRef{Name: "demo"},
		AI:      manifest.AIRef{Agent: "codex"},
	}); err != nil {
		t.Fatal(err)
	}
	return skill.Service{
		Base:         base,
		Agent:        "codex",
		ManifestPath: mPath,
		LockPath:     filepath.Join(base, skill.LockDir, skill.LockFilename),
	}, mPath
}

func wizardCandidate(t *testing.T, id, name, source, sourceRef, version string) skill.Candidate {
	t.Helper()
	root := name
	if source == string(skill.SourceRecipe) {
		root = "skills/" + name
	}
	doc := "---\nname: " + name + "\ndescription: descrição de " + name + "\nmetadata:\n  version: \"" + version + "\"\n---\n# " + name + "\n"
	return skill.Candidate{
		ID:          id,
		Name:        name,
		Description: "descrição de " + name,
		Version:     *mustVersion(t, version),
		Source:      skill.SourceKind(source),
		SourceRef:   sourceRef,
		Path:        name,
		Tree: skill.Tree{FS: fstest.MapFS{
			root + "/SKILL.md": &fstest.MapFile{Data: []byte(doc)},
		}, Root: root},
	}
}

func mustVersion(t *testing.T, v string) *semver.Version {
	t.Helper()
	sv, err := semver.StrictNewVersion(v)
	if err != nil {
		t.Fatal(err)
	}
	return sv
}

func runSkillsFixture(t *testing.T, svc skill.Service, sel *fakeSelector, bundled []skill.Candidate, remote []skill.Candidate, remoteErr error) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	tty := true
	opts := skillsOptions{
		manifestPath:  svc.ManifestPath,
		out:           &out,
		errOut:        &errOut,
		stdin:         bytes.NewReader(nil),
		recipeCatalog: fakeCatalog{cands: bundled},
		selector:      sel,
		stdinTTY:      &tty,
		loadGitHub: func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
			cleanup := func() {}
			if remoteErr != nil {
				return nil, nil, cleanup, remoteErr
			}
			return remote, nil, cleanup, nil
		},
	}
	err := runSkills(opts)
	return out.String(), err
}

// --- tests ---

func TestRunSkillsAllInstalledNoWizard(t *testing.T) {
	svc, _ := skillsProject(t)
	bundled := []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")}
	if _, err := svc.Install(nil, skill.NewLock(), bundled[0]); err != nil {
		t.Fatal(err)
	}
	sel := &fakeSelector{ids: []string{"should-not-be-asked"}}
	out, err := runSkillsFixture(t, svc, sel, bundled, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.gotOptions) != 0 {
		t.Errorf("wizard offered %d options, want none", len(sel.gotOptions))
	}
	if !strings.Contains(out, "nada a fazer") {
		t.Errorf("output missing no-op message: %s", out)
	}
}

func TestRunSkillsInstallsSelection(t *testing.T) {
	svc, _ := skillsProject(t)
	bundled := []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")}
	remote := []skill.Candidate{wizardCandidate(t, "github:willsantos/skills_AI/local-pr-review", "local-pr-review", "github", "willsantos/skills_AI", "1.1.0")}
	sel := &fakeSelector{ids: []string{bundled[0].ID, remote[0].ID}}
	out, err := runSkillsFixture(t, svc, sel, bundled, remote, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.gotOptions) != 2 {
		t.Fatalf("wizard offered %d options, want 2", len(sel.gotOptions))
	}
	if !strings.Contains(sel.gotTitle, "Selecione") {
		t.Errorf("unexpected wizard title %q", sel.gotTitle)
	}
	// Installed lines carry the target.
	if !strings.Contains(out, "code-review 1.0.0 instalada em .codex/skills/code-review") {
		t.Errorf("output missing bundled install line: %s", out)
	}
	if !strings.Contains(out, "local-pr-review 1.1.0 instalada em .codex/skills/local-pr-review") {
		t.Errorf("output missing remote install line: %s", out)
	}
	// Lock persisted with both entries.
	lock, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Skills) != 2 {
		t.Fatalf("lock has %d entries, want 2", len(lock.Skills))
	}
	// Manifest promoted to v2 with the managed ref.
	m, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || len(m.Skills.Managed) != 1 {
		t.Errorf("manifest v%d managed=%v, want v2 with 1 managed", m.Version, m.Skills.Managed)
	}
}

func TestRunSkillsShowsInstalledAndOfferOnlyNew(t *testing.T) {
	svc, _ := skillsProject(t)
	bundled := []skill.Candidate{
		wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0"),
		wizardCandidate(t, "recipe:dotnet/dotnet/aspnet", "aspnet", "recipe", "dotnet", "1.0.0"),
	}
	if _, err := svc.Install(nil, skill.NewLock(), bundled[0]); err != nil {
		t.Fatal(err)
	}
	sel := &fakeSelector{ids: []string{bundled[1].ID}}
	out, err := runSkillsFixture(t, svc, sel, bundled, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Skills já instaladas") || !strings.Contains(out, "code-review") {
		t.Errorf("installed section missing: %s", out)
	}
	if len(sel.gotOptions) != 1 || sel.gotOptions[0].ID != bundled[1].ID {
		t.Errorf("options = %+v, want only aspnet", sel.gotOptions)
	}
}

func TestRunSkillsEmptySelectionIsSuccess(t *testing.T) {
	svc, _ := skillsProject(t)
	bundled := []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")}
	sel := &fakeSelector{}
	out, err := runSkillsFixture(t, svc, sel, bundled, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nenhuma skill selecionada") {
		t.Errorf("output missing empty-selection message: %s", out)
	}
	if _, err := skill.ReadLock(svc.LockPath); !errors.Is(err, skill.ErrLockNotFound) {
		t.Errorf("lock created on empty selection: %v", err)
	}
}

func TestRunSkillsCancelIsGraceful(t *testing.T) {
	svc, _ := skillsProject(t)
	bundled := []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")}
	sel := &fakeSelector{err: errors.New("user aborted")}
	out, err := runSkillsFixture(t, svc, sel, bundled, nil, nil)
	if err != nil {
		t.Fatalf("cancel should not fail: %v", err)
	}
	if !strings.Contains(out, "cancelado") {
		t.Errorf("output missing cancel message: %s", out)
	}
}

func TestRunSkillsNonInteractiveFails(t *testing.T) {
	svc, _ := skillsProject(t)
	var out, errOut bytes.Buffer
	tty := false
	opts := skillsOptions{
		manifestPath: svc.ManifestPath,
		out:          &out,
		errOut:       &errOut,
		stdin:        bytes.NewReader(nil),
		recipeCatalog: fakeCatalog{cands: []skill.Candidate{
			wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0"),
		}},
		stdinTTY: &tty,
	}
	err := runSkills(opts)
	if err == nil || !strings.Contains(err.Error(), "stdin não interativo") {
		t.Fatalf("err = %v, want non-interactive failure", err)
	}
	if _, err := skill.ReadLock(svc.LockPath); !errors.Is(err, skill.ErrLockNotFound) {
		t.Errorf("lock created despite non-interactive failure: %v", err)
	}
}

func TestRunSkillsGitHubUnavailableStillInstallsBundled(t *testing.T) {
	svc, _ := skillsProject(t)
	bundled := []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")}
	sel := &fakeSelector{ids: []string{bundled[0].ID}}
	out, err := runSkillsFixture(t, svc, sel, bundled, nil, errors.New("network down"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "indisponível") {
		t.Errorf("output missing degradation warning: %s", out)
	}
	if _, err := skill.ReadLock(svc.LockPath); err != nil {
		t.Errorf("bundled install failed without GitHub: %v", err)
	}
}

func TestRunSkillsConflictFailsWithoutTouchingDestination(t *testing.T) {
	svc, _ := skillsProject(t)
	cand := wizardCandidate(t, "github:willsantos/skills_AI/local-pr-review", "local-pr-review", "github", "willsantos/skills_AI", "1.0.0")
	dest := filepath.Join(svc.Base, ".codex", "skills", "local-pr-review")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "user-notes.md"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	sel := &fakeSelector{ids: []string{cand.ID}}
	_, err := runSkillsFixture(t, svc, sel, nil, []skill.Candidate{cand}, nil)
	if err == nil {
		t.Fatal("expected conflict failure")
	}
	b, readErr := os.ReadFile(filepath.Join(dest, "user-notes.md"))
	if readErr != nil || string(b) != "keep me" {
		t.Errorf("destination touched by failed install: %q, %v", b, readErr)
	}
}

func TestSkillsCommandRegistered(t *testing.T) {
	root := newRoot("test")
	cmd, _, err := root.Find([]string{"skills"})
	if err != nil || cmd.Name() != "skills" {
		t.Fatalf("skills command not registered: %v", err)
	}
	if cmd.Flags().Lookup("manifest") == nil {
		t.Error("skills command lacks --manifest flag")
	}
}

// --- oro skills update ---

func runUpdateFixture(t *testing.T, svc skill.Service, bundled []skill.Candidate, remote []skill.Candidate, remoteErr error, force, dryRun bool) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	opts := skillsUpdateOptions{
		manifestPath:  svc.ManifestPath,
		force:         force,
		dryRun:        dryRun,
		out:           &out,
		errOut:        &errOut,
		recipeCatalog: fakeCatalog{cands: bundled},
		loadGitHub: func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
			if remoteErr != nil {
				return nil, nil, func() {}, remoteErr
			}
			return remote, nil, func() {}, nil
		},
	}
	err := runSkillsUpdate(opts)
	return out.String(), err
}

func TestRunSkillsUpdateAppliesNewVersion(t *testing.T) {
	svc, _ := skillsProject(t)
	lock := skill.NewLock()
	if _, err := svc.Install(nil, lock, wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	out, err := runUpdateFixture(t, svc, []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.1.0")}, nil, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "code-review") || !strings.Contains(out, "1.0.0 → 1.1.0") {
		t.Errorf("report missing update line: %s", out)
	}
	if !strings.Contains(out, "1 atualizada(s)") {
		t.Errorf("report missing totals: %s", out)
	}
	lockOnDisk, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := lockOnDisk.Lookup("recipe:dotnet/base/code-review")
	if entry.Version != "1.1.0" {
		t.Errorf("lock version = %s", entry.Version)
	}
}

func TestRunSkillsUpdateCurrentOnly(t *testing.T) {
	svc, _ := skillsProject(t)
	lock := skill.NewLock()
	if _, err := svc.Install(nil, lock, wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	out, err := runUpdateFixture(t, svc, []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")}, nil, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(atual)") {
		t.Errorf("report missing current line: %s", out)
	}
}

func TestRunSkillsUpdateBlockedByDrift(t *testing.T) {
	svc, _ := skillsProject(t)
	lock := skill.NewLock()
	if _, err := svc.Install(nil, lock, wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(svc.Base, ".codex", "skills", "code-review")
	if err := os.WriteFile(filepath.Join(dest, "custom.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	upstream := []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.1.0")}

	out, err := runUpdateFixture(t, svc, upstream, nil, nil, false, false)
	if err == nil || !strings.Contains(err.Error(), "bloqueada") {
		t.Fatalf("err = %v, want blocked exit", err)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("report should hint --force: %s", out)
	}
	// Customization untouched.
	if _, err := os.Stat(filepath.Join(dest, "custom.md")); err != nil {
		t.Error("blocked update touched the destination")
	}

	// --force replaces.
	out, err = runUpdateFixture(t, svc, upstream, nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1.0.0 → 1.1.0") {
		t.Errorf("forced report missing update line: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "custom.md")); !os.IsNotExist(err) {
		t.Error("forced update kept local customization")
	}
}

func TestRunSkillsUpdateDryRunHasNoDiff(t *testing.T) {
	svc, _ := skillsProject(t)
	lock := skill.NewLock()
	if _, err := svc.Install(nil, lock, wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(svc.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runUpdateFixture(t, svc, []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.1.0")}, nil, nil, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dry-run:") || !strings.Contains(out, "1.0.0 → 1.1.0") {
		t.Errorf("dry-run report incomplete: %s", out)
	}
	lockAfter, _ := os.ReadFile(svc.LockPath)
	if string(lockBefore) != string(lockAfter) {
		t.Error("dry-run rewrote the lock")
	}
	data, _ := os.ReadFile(filepath.Join(svc.Base, ".codex", "skills", "code-review", "SKILL.md"))
	if strings.Contains(string(data), "1.1.0") {
		t.Error("dry-run rewrote the destination")
	}
}

func TestRunSkillsUpdateRemoteFailureFailsManagedItem(t *testing.T) {
	svc, _ := skillsProject(t)
	lock := skill.NewLock()
	m, err := manifest.Read(svc.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	bundledCand := wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")
	remoteCand := wizardCandidate(t, "github:willsantos/skills_AI/local-pr-review", "local-pr-review", "github", "willsantos/skills_AI", "1.0.0")
	if _, err := svc.Install(m, lock, bundledCand); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(m, lock, remoteCand); err != nil {
		t.Fatal(err)
	}
	// Bundled moves to 1.1.0; GitHub catalog is unreachable.
	out, err := runUpdateFixture(t, svc, []skill.Candidate{wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.1.0")}, nil, errors.New("offline"), false, false)
	if err == nil {
		t.Fatal("expected exit failure for the offline managed item")
	}
	if !strings.Contains(out, "catálogo github:willsantos/skills_AI indisponível") {
		t.Errorf("report missing degradation warning: %s", out)
	}
	// The bundled update was persisted (best-effort).
	lockOnDisk, _ := skill.ReadLock(svc.LockPath)
	entry, _ := lockOnDisk.Lookup(bundledCand.ID)
	if entry.Version != "1.1.0" {
		t.Errorf("bundled version = %s, want 1.1.0", entry.Version)
	}
	// The managed destination was not removed.
	if _, err := os.Stat(filepath.Join(svc.Base, ".codex", "skills", "local-pr-review")); err != nil {
		t.Error("managed destination removed on catalog failure")
	}
}

func TestRunSkillsUpdateNoLockIsNoOp(t *testing.T) {
	svc, _ := skillsProject(t)
	out, err := runUpdateFixture(t, svc, nil, nil, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nenhuma skill gerenciada") {
		t.Errorf("output missing no-lock message: %s", out)
	}
}

func TestSkillsUpdateCommandRegistered(t *testing.T) {
	root := newRoot("test")
	cmd, _, err := root.Find([]string{"skills", "update"})
	if err != nil || cmd.Name() != "update" {
		t.Fatalf("skills update not registered: %v", err)
	}
	for _, flag := range []string{"force", "dry-run", "manifest"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing flag --%s", flag)
		}
	}
}

// Regression (review 2026-09-09): `oro skills update` must run the lazy
// migration even when the lock is empty/absent — legacy intact bundled skills
// are adopted into the lock (FR-20).
func TestRunSkillsUpdateAdoptsLegacyWithoutLock(t *testing.T) {
	svc, _ := skillsProject(t)
	// Legacy state: bundled skill installed before the lock existed.
	dest := filepath.Join(svc.Base, ".codex", "skills", "code-review")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("---\nname: code-review\ndescription: descrição de code-review\nmetadata:\n  version: \"1.0.0\"\n---\n# code-review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cand := wizardCandidate(t, "recipe:dotnet/base/code-review", "code-review", "recipe", "dotnet", "1.0.0")
	out, err := runUpdateFixture(t, svc, []skill.Candidate{cand}, nil, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(atual)") {
		t.Errorf("output missing current line: %s", out)
	}
	lock, err := skill.ReadLock(svc.LockPath)
	if err != nil {
		t.Fatalf("lock not created by migration: %v", err)
	}
	if _, ok := lock.Lookup(cand.ID); !ok {
		t.Errorf("adopted entry missing: %+v", lock.Skills)
	}
}

func TestRunSkillsUpdateEmptyCatalogNoResults(t *testing.T) {
	svc, _ := skillsProject(t)
	out, err := runUpdateFixture(t, svc, nil, nil, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nenhuma skill gerenciada") {
		t.Errorf("output missing empty message: %s", out)
	}
}
