package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func onboardingRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	c := exec.Command("git", "-C", d, "init")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git init: %s %v", b, e)
	}
	return d
}
func onboardingGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %s %v", args, b, e)
	}
	return string(b)
}
func executeOnboarding(t *testing.T, args ...string) (string, error) {
	return executeRootCommand(t, append([]string{"onboarding"}, args...)...)
}
func executeRootCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestOnboardingPlanAndApplyDryRunAreReadOnly(t *testing.T) {
	d := onboardingRepo(t)
	out, e := executeOnboarding(t, "plan", "--path", d, "--protect-branches", "main,develop")
	if e != nil {
		t.Fatalf("plan: %v\n%s", e, out)
	}
	for _, want := range []string{"read-only preview", filepath.Join(d, ".gitignore"), "pre-commit", "pre-merge-commit", "main, develop", "--no-verify", "Conflicts: none"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q: %s", want, out)
		}
	}
	for _, path := range []string{filepath.Join(d, ".gitignore"), filepath.Join(d, ".git", "orotools-onboarding-state-v1.json"), filepath.Join(d, ".git", "hooks", "pre-commit"), filepath.Join(d, ".git", "hooks", "pre-merge-commit")} {
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			t.Errorf("plan wrote %s", path)
		}
	}
	out, e = executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main,develop", "--dry-run")
	if e != nil {
		t.Fatalf("apply dry-run: %v\n%s", e, out)
	}
	if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
		t.Fatal("dry-run wrote ignore")
	}
}

func TestOnboardingRequiresExplicitApplyAndUninstallOptIn(t *testing.T) {
	d := onboardingRepo(t)
	out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main,develop")
	if e == nil || !strings.Contains(e.Error(), "pass --yes") {
		t.Fatalf("expected explicit opt-in error, got %v", e)
	}
	if !strings.Contains(out, "pre-commit") {
		t.Fatalf("preview missing: %s", out)
	}
	if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
		t.Fatal("apply without yes wrote files")
	}
	if _, e = executeOnboarding(t, "apply", "--path", d, "--yes", "--dry-run"); e == nil {
		t.Fatal("--yes + --dry-run accepted")
	}
	if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
		t.Fatal("invalid flags wrote files")
	}
	if out, e = executeOnboarding(t, "uninstall", "--path", d); e == nil || !strings.Contains(e.Error(), "uninstall blocked") || !strings.Contains(out, "ownership state") {
		t.Fatalf("expected missing ownership conflict in uninstall preview: %v\n%s", e, out)
	}
	if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
		t.Fatal("uninstall preview wrote files")
	}
}

func TestOnboardingExplicitEmptyProtectionFlagFailsBeforeWrites(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		d := onboardingRepo(t)
		for _, command := range [][]string{{"plan"}, {"apply", "--yes"}} {
			args := append([]string{}, command...)
			args = append(args, "--path", d, "--protect-branches="+raw)
			_, err := executeOnboarding(t, args...)
			if err == nil || !strings.Contains(err.Error(), "explicitly set but is empty") {
				t.Fatalf("empty protection accepted for %v: %v", args, err)
			}
			for _, path := range []string{filepath.Join(d, ".gitignore"), filepath.Join(d, ".git", "orotools-onboarding-state-v1.json")} {
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatalf("empty protection wrote %s", path)
				}
			}
		}
	}
}

func TestOnboardingPreviewsShareApplyAndUninstallPreflight(t *testing.T) {
	d := onboardingRepo(t)
	apply := func(branch string) {
		t.Helper()
		out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", branch, "--yes")
		if e != nil {
			t.Fatalf("apply %s: %v\n%s", branch, e, out)
		}
	}
	apply("main")
	for _, args := range [][]string{{"plan", "--protect-branches", "develop"}, {"plan"}} {
		out, e := executeOnboarding(t, append(args, "--path", d)...)
		if e == nil || !strings.Contains(out, "options differ from installed ownership state") {
			t.Fatalf("preview failed to report apply options conflict %v: %v\n%s", args, e, out)
		}
	}
	ignore := filepath.Join(d, ".gitignore")
	b, e := os.ReadFile(ignore)
	if e != nil {
		t.Fatal(e)
	}
	edited := append(append([]byte{}, b...), []byte("user edit without markers\n")...)
	if e = os.WriteFile(ignore, edited, 0644); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"plan", "--protect-branches", "main"}, {"uninstall", "--yes"}} {
		out, e := executeOnboarding(t, append(args, "--path", d)...)
		if e == nil || !strings.Contains(out, "managed .gitignore drift detected") {
			t.Fatalf("preview failed to report drift %v: %v\n%s", args, e, out)
		}
	}
	after, _ := os.ReadFile(ignore)
	if !bytes.Equal(after, edited) {
		t.Fatal("read-only previews modified drifted ignore")
	}
}

func TestOnboardingPreviewsRejectMissingOwnedHooksWithoutEffects(t *testing.T) {
	for _, missing := range []string{"pre-commit", "pre-merge-commit"} {
		t.Run(missing, func(t *testing.T) {
			d := onboardingRepo(t)
			out, err := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main", "--yes")
			if err != nil {
				t.Fatalf("apply fixture: %v\n%s", err, out)
			}
			missingPath := filepath.Join(d, ".git", "hooks", missing)
			if err = os.Remove(missingPath); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"plan", "--protect-branches", "main"}, {"apply", "--protect-branches", "main", "--dry-run"}, {"uninstall"}} {
				out, err = executeOnboarding(t, append(args, "--path", d)...)
				if err == nil || !strings.Contains(out, missing+" managed hook is missing") {
					t.Fatalf("preview accepted missing owned hook, args=%v: %v\n%s", args, err, out)
				}
				if _, statErr := os.Stat(filepath.Join(d, ".gitignore")); statErr != nil {
					t.Fatalf("preview changed ignore: %v", statErr)
				}
				if _, statErr := os.Stat(filepath.Join(d, ".git", "orotools-onboarding-state-v1.json")); statErr != nil {
					t.Fatalf("preview changed ownership state: %v", statErr)
				}
			}
		})
	}
}

func TestIgnoreOnlyPreviewRejectsCustomPreMergeHookWithoutEffects(t *testing.T) {
	d := onboardingRepo(t)
	hookPath := filepath.Join(d, ".git", "hooks", "pre-merge-commit")
	custom := []byte("#!/bin/sh\necho custom\n")
	if err := os.WriteFile(hookPath, custom, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"plan"}, {"apply", "--dry-run"}} {
		out, err := executeOnboarding(t, append(args, "--path", d)...)
		if err == nil || !strings.Contains(out, "pre-merge-commit hook exists or drifted") {
			t.Fatalf("custom hook conflict not shown, args=%v: %v\n%s", args, err, out)
		}
		if _, statErr := os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(statErr) {
			t.Fatalf("preview wrote ignore: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(d, ".git", "orotools-onboarding-state-v1.json")); !os.IsNotExist(statErr) {
			t.Fatalf("preview wrote state: %v", statErr)
		}
		got, readErr := os.ReadFile(hookPath)
		if readErr != nil || !bytes.Equal(got, custom) {
			t.Fatalf("preview altered custom hook: %v", readErr)
		}
	}
}

func TestOnboardingApplyWithoutProtectionInstallsNoHooks(t *testing.T) {
	d := onboardingRepo(t)
	out, e := executeOnboarding(t, "apply", "--path", d, "--yes")
	if e != nil {
		t.Fatalf("apply: %v\n%s", e, out)
	}
	for _, n := range []string{"pre-commit", "pre-merge-commit"} {
		if _, e = os.Stat(filepath.Join(d, ".git", "hooks", n)); !os.IsNotExist(e) {
			t.Fatalf("hook %s installed without opt-in", n)
		}
	}
	if _, e = os.Stat(filepath.Join(d, ".gitignore")); e != nil {
		t.Fatal("explicit apply did not manage gitignore")
	}
}

func TestInitDoesNotInstallOnboardingAutomatically(t *testing.T) {
	d := onboardingRepo(t)
	restore := chdir(t, d)
	defer restore()
	out, e := executeRootCommand(t, "init", "--yes")
	if e != nil {
		t.Fatalf("init: %v\n%s", e, out)
	}
	for _, n := range []string{"pre-commit", "pre-merge-commit"} {
		if _, e = os.Stat(filepath.Join(d, ".git", "hooks", n)); !os.IsNotExist(e) {
			t.Fatalf("init installed hook %s", n)
		}
	}
	if _, e = os.Stat(filepath.Join(d, "orotools.yaml")); e != nil {
		t.Fatal("init default manifest was changed")
	}
}

func TestOnboardingApplyInstallsExplicitRealHooksAndUninstallRestores(t *testing.T) {
	d := onboardingRepo(t)
	onboardingGit(t, d, "symbolic-ref", "HEAD", "refs/heads/main")
	out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main", "--yes")
	if e != nil {
		t.Fatalf("apply: %v\n%s", e, out)
	}
	if !strings.Contains(out, "Onboarding applied") {
		t.Fatalf("missing result: %s", out)
	}
	for _, name := range []string{"pre-commit", "pre-merge-commit"} {
		fi, e := os.Stat(filepath.Join(d, ".git", "hooks", name))
		if e != nil {
			t.Fatalf("hook %s missing: %v", name, e)
		}
		if fi.Mode().Perm() != 0755 {
			t.Fatalf("hook mode=%v", fi.Mode().Perm())
		}
	}
	c := exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "blocked")
	if b, e := c.CombinedOutput(); e == nil || !strings.Contains(string(b), "direct commit blocked") {
		t.Fatalf("real hook did not block main commit: %s %v", b, e)
	}
	before, _ := os.ReadFile(filepath.Join(d, ".gitignore"))
	if _, e = executeOnboarding(t, "uninstall", "--path", d); e == nil {
		t.Fatal("uninstall without yes succeeded")
	}
	after, _ := os.ReadFile(filepath.Join(d, ".gitignore"))
	if !bytes.Equal(before, after) {
		t.Fatal("uninstall preview changed ignore")
	}
	out, e = executeOnboarding(t, "uninstall", "--path", d, "--yes")
	if e != nil {
		t.Fatalf("uninstall: %v\n%s", e, out)
	}
	if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
		t.Fatalf("originally absent ignore remains: %v", e)
	}
	for _, name := range []string{"pre-commit", "pre-merge-commit"} {
		if _, e = os.Stat(filepath.Join(d, ".git", "hooks", name)); !os.IsNotExist(e) {
			t.Fatalf("hook %s remains: %v", name, e)
		}
	}
}

func TestOnboardingConflictsPathsAndDriftHaveNoEffects(t *testing.T) {
	t.Run("outside-git", func(t *testing.T) {
		d := t.TempDir()
		out, e := executeOnboarding(t, "apply", "--path", d, "--yes")
		if e == nil || !strings.Contains(e.Error(), "not a Git worktree") {
			t.Fatalf("expected git diagnostic, got %v %s", e, out)
		}
		entries, _ := os.ReadDir(d)
		if len(entries) != 0 {
			t.Fatal("non-repository path modified")
		}
	})
	t.Run("existing-hook", func(t *testing.T) {
		d := onboardingRepo(t)
		hook := filepath.Join(d, ".git", "hooks", "pre-commit")
		local := []byte("#!/bin/sh\necho local\n")
		if e := os.WriteFile(hook, local, 0755); e != nil {
			t.Fatal(e)
		}
		out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main", "--yes")
		if e == nil || !strings.Contains(out, "Conflicts:") {
			t.Fatalf("conflict not shown: %v %s", e, out)
		}
		if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
			t.Fatal("conflict partially changed ignore")
		}
		got, _ := os.ReadFile(hook)
		if !bytes.Equal(got, local) {
			t.Fatal("existing hook overwritten")
		}
	})
	t.Run("hooks-path", func(t *testing.T) {
		d := onboardingRepo(t)
		onboardingGit(t, d, "config", "core.hooksPath", ".custom-hooks")
		out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main", "--yes")
		if e == nil || !strings.Contains(out, "core.hooksPath") {
			t.Fatalf("hooksPath conflict not reported: %v %s", e, out)
		}
		if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
			t.Fatal("hooksPath conflict changed ignore")
		}
	})
	t.Run("invalid-branch", func(t *testing.T) {
		d := onboardingRepo(t)
		out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "bad*branch", "--yes")
		if e == nil {
			t.Fatal("invalid branch accepted")
		}
		if _, e = os.Stat(filepath.Join(d, ".gitignore")); !os.IsNotExist(e) {
			t.Fatal("invalid branch modified project")
		}
		_ = out
	})
	t.Run("drifted-uninstall", func(t *testing.T) {
		d := onboardingRepo(t)
		out, e := executeOnboarding(t, "apply", "--path", d, "--protect-branches", "main", "--yes")
		if e != nil {
			t.Fatalf("apply: %v %s", e, out)
		}
		ignore := filepath.Join(d, ".gitignore")
		if e = os.WriteFile(ignore, []byte("user edit\n"), 0644); e != nil {
			t.Fatal(e)
		}
		before, _ := os.ReadFile(ignore)
		if out, e = executeOnboarding(t, "uninstall", "--path", d, "--yes"); e == nil || !strings.Contains(out, "Conflicts:") {
			t.Fatalf("drift not blocked: %v %s", e, out)
		}
		after, _ := os.ReadFile(ignore)
		if !bytes.Equal(before, after) {
			t.Fatal("drifted ignore changed")
		}
		if _, e = os.Stat(filepath.Join(d, ".git", "hooks", "pre-commit")); e != nil {
			t.Fatal("hook removed despite drift")
		}
	})
}

func TestOnboardingCommandIsRegisteredAndUnknownFlagsFailClosed(t *testing.T) {
	if _, e := executeOnboarding(t, "plan", "--unknown"); e == nil {
		t.Fatal("unknown flag accepted")
	}
	root := newRoot("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"onboarding", "plan", "--help"})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "somente leitura") {
		t.Fatalf("help missing onboarding semantics: %s", out.String())
	}
}
