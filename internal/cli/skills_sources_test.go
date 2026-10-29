package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

func sourceFixture(t *testing.T) (sourceCommandOptions, skill.Candidate) {
	t.Helper()
	svc, manifestPath := skillsProject(t)
	candidate := wizardCandidate(t, "github:willsantos/skills_AI/same-name", "same-name", "github", "willsantos/skills_AI", "1.2.3")
	opts := sourceCommandOptions{
		manifestPath:     manifestPath,
		manifestExplicit: true,
		out:              &bytes.Buffer{},
		loadGitHub: func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
			return []skill.Candidate{candidate}, nil, func() {}, nil
		},
	}
	_ = svc
	return opts, candidate
}

func TestSkillsLocalCheckoutListInspectAndInstallDisposable(t *testing.T) {
	opts, _ := sourceFixture(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "local-docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: local-docs\ndescription: local fixture\nmetadata:\n  version: \"1.0.0\"\n---\n# Local fixture\n"
	if err := os.WriteFile(filepath.Join(root, "local-docs", "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	opts.localRoots = []string{root}
	opts.loadGitHub = func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
		return nil, nil, func() {}, errors.New("network must not be used")
	}
	if err := runSkillsSourceList(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	id := "local:" + localCheckoutSourceRef(root) + "/local-docs"
	opts.id = id
	if err := runSkillsSourceInspect(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.yes = true
	if err := runSkillsSourceInstall(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.manifestPath), ".codex", "skills", "local-docs", "SKILL.md")); err != nil {
		t.Fatalf("local install missing target: %v", err)
	}
}

func TestSkillsLocalCheckoutRejectsDirtyUnauthorizedContent(t *testing.T) {
	opts, _ := sourceFixture(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skill", "SKILL.md"), []byte("---\nname: skill\ndescription: test\nmetadata:\n  version: \"1.0.0\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "skill", "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.localRoots = []string{root}
	if err := runSkillsSourceList(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "não é uma origem limpa") {
		t.Fatalf("dirty local checkout must be rejected: %v", err)
	}
}

func TestSkillsSourceInspectPublicExactIDAndReadOnly(t *testing.T) {
	opts, candidate := sourceFixture(t)
	opts.id = candidate.ID
	before, err := os.ReadDir(filepath.Dir(opts.manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := runSkillsSourceInspect(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if got := opts.out.(*bytes.Buffer).String(); !strings.Contains(got, "id="+candidate.ID) || !strings.Contains(got, "target=.codex/skills/same-name") || !strings.Contains(got, "version=1.2.3") {
		t.Fatalf("inspect missing fields: %s", got)
	}
	after, err := os.ReadDir(filepath.Dir(opts.manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("inspect mutated project entries: before=%v after=%v", before, after)
	}
}

func TestSkillsSourceMissingIDDoesNotFallbackToHomonym(t *testing.T) {
	opts, candidate := sourceFixture(t)
	opts.id = "local:checkout-test/same-name"
	if err := runSkillsSourceInspect(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "checkout autorizado") {
		t.Fatalf("local identity without authorization must fail closed, got %v", err)
	}
	opts.id = "github:willsantos/skills_AI/missing"
	if err := runSkillsSourceInspect(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "não encontrada") {
		t.Fatalf("missing exact ID should not fall back to homonym %q, got %v", candidate.Name, err)
	}
}

func TestSkillsSourceListPublicIsReadOnlyAndShowsDestination(t *testing.T) {
	opts, candidate := sourceFixture(t)
	if err := runSkillsSourceList(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	got := opts.out.(*bytes.Buffer).String()
	for _, want := range []string{candidate.ID, "github:", "version=1.2.3", "target=.codex/skills/same-name", "decision=install"} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q: %s", want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.manifestPath), skill.LockDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only list created lock state: %v", err)
	}
}

func TestSkillsSourceInstallDryRunAndConflictDoNotMutate(t *testing.T) {
	opts, candidate := sourceFixture(t)
	opts.id, opts.dryRun = candidate.ID, true
	if err := runSkillsSourceInstall(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(opts.manifestPath)
	target := filepath.Join(base, ".codex", "skills", candidate.Name)
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, skill.LockDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created lock: %v", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	opts.dryRun = false
	opts.yes = true
	if err := runSkillsSourceInstall(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "conflito") {
		t.Fatalf("expected destination conflict, got %v", err)
	}
}

type reviewHookWriter struct {
	bytes.Buffer
	hook  func()
	fired bool
}

func (w *reviewHookWriter) Write(p []byte) (int, error) {
	n, e := w.Buffer.Write(p)
	if !w.fired && strings.Contains(w.String(), "first 1.0.0 instalada em") {
		w.fired = true
		w.hook()
	}
	return n, e
}

func TestReviewWizardConcurrentExternalMutation(t *testing.T) {
	for _, mode := range []string{"source", "manifest", "lock"} {
		t.Run(mode, func(t *testing.T) {
			svc, mp := skillsProject(t)
			first := wizardCandidate(t, "github:willsantos/skills_AI/first", "first", "github", "willsantos/skills_AI", "1.0.0")
			second := wizardCandidate(t, "github:willsantos/skills_AI/second", "second", "github", "willsantos/skills_AI", "1.0.0")
			// Mutable filesystem models the live authorized local checkout used by wizard.
			source := t.TempDir()
			if err := os.Mkdir(filepath.Join(source, "second"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "second", "SKILL.md"), []byte("approved-source"), 0644); err != nil {
				t.Fatal(err)
			}
			second.Tree = skill.Tree{FS: os.DirFS(source), Root: "second"}
			out := &reviewHookWriter{}
			out.hook = func() {
				done := make(chan error, 1)
				go func() {
					switch mode {
					case "source":
						done <- os.WriteFile(filepath.Join(source, "second", "SKILL.md"), []byte("EXTERNAL-UNAPPROVED-CONTENT"), 0644)
					case "manifest":
						data, err := os.ReadFile(mp)
						if err == nil {
							err = os.WriteFile(mp, append(data, []byte("\n# EXTERNAL CHANGE\n")...), 0644)
						}
						done <- err
					case "lock":
						data, err := os.ReadFile(svc.LockPath)
						if err == nil {
							err = os.WriteFile(svc.LockPath, append(data, []byte("\n# EXTERNAL CHANGE\n")...), 0644)
						}
						done <- err
					}
				}()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			tty := true
			err := runSkills(skillsOptions{manifestPath: mp, out: out, stdinTTY: &tty, selector: &fakeSelector{ids: []string{first.ID, second.ID}}, recipeCatalog: fakeCatalog{}, loadGitHub: func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
				return []skill.Candidate{first, second}, nil, func() {}, nil
			}})
			if !out.fired {
				t.Fatalf("hook not reached: %s", out.String())
			}
			content, readErr := os.ReadFile(filepath.Join(svc.Base, ".codex", "skills", "second", "SKILL.md"))
			t.Logf("mode=%s err=%v secondInstalled=%v content=%q", mode, err, readErr == nil, content)
			if err == nil && readErr == nil {
				t.Errorf("UNSAFE: external %s mutation silently incorporated after first apply", mode)
			}
		})
	}
}

type reviewSelectorFunc func([]skillOption) ([]string, error)

func (f reviewSelectorFunc) Select(_ string, opts []skillOption) ([]string, error) { return f(opts) }
func TestReviewWizardLocalCheckoutChangesWhilePicking(t *testing.T) {
	svc, mp := skillsProject(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "local-docs"), 0755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: local-docs\ndescription: local fixture\nmetadata:\n  version: \"1.0.0\"\n---\n# Approved\n"
	file := filepath.Join(root, "local-docs", "SKILL.md")
	if err := os.WriteFile(file, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, data)
		}
	}
	id := "local:" + localCheckoutSourceRef(root) + "/local-docs"
	tty := true
	var out bytes.Buffer
	sel := reviewSelectorFunc(func(opts []skillOption) ([]string, error) {
		if err := os.WriteFile(file, []byte(doc+"\nUNAPPROVED DIRTY CONTENT\n"), 0644); err != nil {
			t.Fatal(err)
		}
		return []string{id}, nil
	})
	err := runSkills(skillsOptions{manifestPath: mp, out: &out, stdinTTY: &tty, selector: sel, localRoots: []string{root}, recipeCatalog: fakeCatalog{}, loadGitHub: func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
		return nil, nil, func() {}, nil
	}})
	data, readErr := os.ReadFile(filepath.Join(svc.Base, ".codex", "skills", "local-docs", "SKILL.md"))
	_, verifyErr := inspectCleanGitCheckout(context.Background(), root)
	t.Logf("wizardError=%v targetError=%v sourceVerifierError=%v installed=%q", err, readErr, verifyErr, data)
	if err == nil && strings.Contains(string(data), "UNAPPROVED DIRTY CONTENT") {
		t.Error("UNSAFE: wizard installed dirty local checkout content under old revision")
	}
}

func TestReviewWizardMultiSkillNoExternalChanges(t *testing.T) {
	svc, _ := skillsProject(t)
	first := wizardCandidate(t, "github:willsantos/skills_AI/first", "first", "github", "willsantos/skills_AI", "1.0.0")
	second := wizardCandidate(t, "github:willsantos/skills_AI/second", "second", "github", "willsantos/skills_AI", "1.0.0")
	out, err := runSkillsFixture(t, svc, &fakeSelector{ids: []string{first.ID, second.ID}}, nil, []skill.Candidate{first, second}, nil)
	if err != nil {
		t.Fatalf("ordinary multi-install: %v %s", err, out)
	}
	for _, name := range []string{"first", "second"} {
		if _, err := os.Stat(filepath.Join(svc.Base, ".codex", "skills", name, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReviewSourcePublicConsentPreviewApply(t *testing.T) {
	opts, candidate := sourceFixture(t)
	opts.id = candidate.ID
	base := filepath.Dir(opts.manifestPath)
	if err := runSkillsSourceInstall(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "preview only") {
		t.Fatalf("missing consent accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, skill.LockDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview mutated state: %v", err)
	}
	opts.yes = true
	opts.dryRun = true
	if err := runSkillsSourceInstall(context.Background(), opts); err == nil {
		t.Fatal("conflicting flags accepted")
	}
	opts.dryRun = false
	if err := runSkillsSourceInstall(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, ".codex", "skills", candidate.Name, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := runSkillsSourceInspect(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.out = &bytes.Buffer{}
	if err := runSkillsSourceInstall(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opts.out.(*bytes.Buffer).String(), "decision=unchanged") {
		t.Fatal("identical install not unchanged")
	}
	if err := os.WriteFile(filepath.Join(base, ".codex", "skills", candidate.Name, "SKILL.md"), []byte("local user drift"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runSkillsSourceInstall(context.Background(), opts); err == nil {
		t.Fatal("drift overwrite accepted")
	}
}

func TestReviewWizardRefreshChangesTargetDecision(t *testing.T) {
	base := t.TempDir()
	svc := skill.Service{Base: base, Agent: "opencode", ManifestPath: filepath.Join(base, "orotools.yaml"), LockPath: filepath.Join(base, skill.LockDir, skill.LockFilename)}
	first := wizardCandidate(t, "github:willsantos/skills_AI/first", "first", "github", "willsantos/skills_AI", "1.0.0")
	second := wizardCandidate(t, "github:willsantos/skills_AI/second", "second", "github", "willsantos/skills_AI", "1.0.0")
	if _, err := svc.Install(nil, skill.NewLock(), second); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, ".codex", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, ".opencode", "skills", "second")
	out := &reviewHookWriter{}
	out.hook = func() {
		if err := os.Rename(target, target+"-external-move"); err != nil {
			t.Fatal(err)
		}
	}
	tty := true
	err := runSkills(skillsOptions{manifestPath: svc.ManifestPath, out: out, stdinTTY: &tty, pickAgents: func([]string) ([]string, error) { return []string{"opencode", "codex"}, nil }, selector: &fakeSelector{ids: []string{first.ID, second.ID}}, loadGitHub: func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error) {
		return []skill.Candidate{first, second}, nil, func() {}, nil
	}})
	_, targetErr := os.Stat(target)
	t.Logf("wizardError=%v externallyMovedTargetRecreated=%v", err, targetErr == nil)
	if !out.fired {
		t.Fatalf("hook not reached: %s", out.String())
	}
	if err == nil && targetErr == nil {
		t.Error("UNSAFE: preflight unchanged silently became install after external target move")
	}
}

func TestReviewLegacyInspectionRejectsSymlinkWithoutStateWrites(t *testing.T) {
	for _, mode := range []string{"target", "state"} {
		t.Run(mode, func(t *testing.T) {
			svc, _ := skillsProject(t)
			cand := wizardCandidate(t, "recipe:dotnet/legacy", "legacy", "recipe", "dotnet", "1.0.0")
			external := t.TempDir()
			sentinel := filepath.Join(external, "keep")
			if err := os.WriteFile(sentinel, []byte("untouched"), 0644); err != nil {
				t.Fatal(err)
			}
			name := ".codex"
			if mode == "state" {
				name = skill.LockDir
			}
			if err := os.Symlink(external, filepath.Join(svc.Base, name)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := svc.InspectBundledMigration(skill.NewLock(), []skill.Candidate{cand}); err == nil {
				t.Fatal("unsafe migration preview accepted")
			}
			entries, err := os.ReadDir(external)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "keep" {
				t.Fatalf("external path changed: %v", entries)
			}
			if mode == "target" {
				if _, err := os.Stat(filepath.Join(svc.Base, skill.LockDir)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("preview created state: %v", err)
				}
			}
		})
	}
}

func TestReviewWizardLegacyAdoptionCancelDoesNotPersist(t *testing.T) {
	svc, _ := skillsProject(t)
	legacy := wizardCandidate(t, "recipe:dotnet/legacy", "legacy", "recipe", "dotnet", "1.0.0")
	fresh := wizardCandidate(t, "github:willsantos/skills_AI/fresh", "fresh", "github", "willsantos/skills_AI", "1.0.0")
	file, err := legacy.Tree.FS.Open(legacy.Tree.Root + "/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, info.Size())
	if _, err := file.Read(data); err != nil {
		t.Fatal(err)
	}
	file.Close()
	dest := filepath.Join(svc.Base, ".codex", "skills", "legacy")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runSkillsFixture(t, svc, &fakeSelector{err: errors.New("cancel")}, []skill.Candidate{legacy}, []skill.Candidate{fresh}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(svc.Base, skill.LockDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancel persisted legacy adoption: %v", err)
	}
}
