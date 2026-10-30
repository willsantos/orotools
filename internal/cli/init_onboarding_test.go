package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	initIgnoreBegin = "# >>> orotools onboarding v1 >>>"
	initStateFile   = "orotools-onboarding-state-v1.json"
)

func assertNoOnboardingArtifacts(t *testing.T, dir string) {
	t.Helper()
	for _, rel := range []string{
		filepath.Join(".git", "hooks", "pre-commit"),
		filepath.Join(".git", "hooks", "pre-merge-commit"),
		filepath.Join(".git", initStateFile),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("onboarding artifact present without opt-in: %s", rel)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil && bytes.Contains(b, []byte(initIgnoreBegin)) {
		t.Error("managed ignore block present without opt-in")
	}
}

func TestRunInit_OnboardingOptOutIdentical(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if strings.Contains(out.String(), "onboarding") {
		t.Errorf("output mentions onboarding without opt-in:\n%s", out.String())
	}
	assertNoOnboardingArtifacts(t, dir)
}

func TestRunInit_OnboardingCleanProjectYes(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: true, onboarding: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !bytes.Contains(b, []byte(initIgnoreBegin)) {
		t.Fatalf("managed ignore block missing: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", initStateFile)); err != nil {
		t.Fatalf("ownership state missing: %v", err)
	}
	// Without --protect-branches no hooks may exist.
	assertNoHooks(t, dir)
	if !strings.Contains(out.String(), "onboarding aplicado") {
		t.Errorf("output missing onboarding success:\n%s", out.String())
	}
}

func TestRunInit_OnboardingWithProtectBranches(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: true, onboarding: true, branches: "main,develop", branchesChanged: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	for _, hook := range []string{"pre-commit", "pre-merge-commit"} {
		b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", hook))
		if err != nil || !bytes.Contains(b, []byte("orotools-managed-hook-v2")) {
			t.Errorf("managed hook %s missing: %q %v", hook, b, err)
		}
	}
}

func assertNoHooks(t *testing.T, dir string) {
	t.Helper()
	for _, hook := range []string{"pre-commit", "pre-merge-commit"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", hook)); !os.IsNotExist(err) {
			t.Errorf("hook %s exists without protection opt-in", hook)
		}
	}
}

func TestRunInit_OnboardingDryRunWritesNothing(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: false, onboarding: true, dryRun: true, branches: "main", branchesChanged: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); !os.IsNotExist(err) {
		t.Error("dry-run wrote the manifest")
	}
	assertNoOnboardingArtifacts(t, dir)
	for _, want := range []string{"read-only preview", "dry-run", "pre-commit"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunInit_OnboardingConflictBlocksActionInitSucceeds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		conflict func(t *testing.T, dir string)
		wantText string
	}{
		{"core-hookspath", func(t *testing.T, dir string) {
			onboardingGit(t, dir, "config", "--local", "core.hooksPath", ".hooks")
		}, "core.hooksPath"},
		{"existing-hook", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\necho local\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "Conflicts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := onboardingRepo(t)
			touch(t, dir, "Gemfile")
			tc.conflict(t, dir)
			var out bytes.Buffer
			if err := runInit(initOptions{yes: true, onboarding: true, branches: "main", branchesChanged: true, out: &out, cwd: dir}); err != nil {
				t.Fatalf("init must succeed when onboarding is blocked: %v\n%s", err, out.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); err != nil {
				t.Fatalf("manifest not written despite blocked onboarding: %v", err)
			}
			if !strings.Contains(out.String(), tc.wantText) {
				t.Errorf("output missing conflict diagnostic %q:\n%s", tc.wantText, out.String())
			}
			if !strings.Contains(out.String(), "não aplicado") {
				t.Errorf("output must state onboarding was not applied:\n%s", out.String())
			}
			// Original configuration is preserved: hooksPath still set, local
			// hook bytes untouched, no managed block written.
			if tc.name == "core-hookspath" {
				if v := onboardingGit(t, dir, "config", "--local", "core.hooksPath"); strings.TrimSpace(v) != ".hooks" {
					t.Errorf("core.hooksPath changed: %q", v)
				}
			}
			if b, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil && bytes.Contains(b, []byte(initIgnoreBegin)) {
				t.Error("managed ignore block written despite conflict")
			}
		})
	}
}

func TestRunInit_OnboardingWithoutYesIsPreviewOnly(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: false, onboarding: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); !os.IsNotExist(err) {
		t.Error("manifest written without --yes")
	}
	assertNoOnboardingArtifacts(t, dir)
	if !strings.Contains(out.String(), "read-only preview") || !strings.Contains(out.String(), "--yes") {
		t.Errorf("preview/hint missing:\n%s", out.String())
	}
}

func TestRunInit_OnboardingNonGitDirStillWritesManifest(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: true, onboarding: true, branches: "main", branchesChanged: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("init must not fail on non-git dir: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if !strings.Contains(out.String(), "não é um repositório Git") {
		t.Errorf("output missing non-git warning:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "não aplicado") {
		t.Errorf("output must state onboarding was not applied:\n%s", out.String())
	}
}

func TestRunInit_OnboardingReInitIsIdempotent(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: true, onboarding: true, branches: "main,develop", branchesChanged: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	firstHook, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runInit(initOptions{yes: true, onboarding: true, branches: "main,develop", branchesChanged: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("second init must succeed: %v\n%s", err, out.String())
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	secondHook, _ := os.ReadFile(filepath.Join(dir, ".git", "hooks", "pre-commit"))
	if string(first) != string(second) {
		t.Error("second init changed .gitignore")
	}
	if string(firstHook) != string(secondHook) {
		t.Error("second init changed managed hook")
	}
}

func TestRunInit_OnboardingFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		opts initOptions
		want string
	}{
		{"protect-without-onboarding", initOptions{yes: true, branches: "main", branchesChanged: true}, "--protect-branches requer --onboarding"},
		{"empty-branches", initOptions{yes: true, onboarding: true, branches: "  ", branchesChanged: true}, "explicitly set but is empty"},
		{"yes-and-dry-run", initOptions{yes: true, dryRun: true, onboarding: true}, "--yes e --dry-run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runInit(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestInitCmd_RegistersOnboardingFlags(t *testing.T) {
	cmd := newInitCmd()
	for _, name := range []string{"yes", "onboarding", "protect-branches", "dry-run"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered on oro init", name)
		}
	}
}
