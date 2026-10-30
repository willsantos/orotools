package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/manifest"
)

// TestRunApply_OnboardingDryRunZeroWritesThenApply covers the M04.3 flow end to
// end through `oro apply`: the dry-run preview lists the onboarding plan but
// writes nothing; the real run applies exclusively through the sealed plan.
func TestRunApply_OnboardingDryRunZeroWritesThenApply(t *testing.T) {
	dir := onboardingRepo(t)
	touch(t, dir, "Gemfile")

	recipeYAML := `version: 1
name: adopt-demo
description: onboarding adoption demo
steps:
  - id: git-onboarding
    type: onboarding
    protect_branches:
      - main
      - develop
`
	recipePath := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(recipePath, []byte(recipeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, manifest.Filename)
	if err := manifest.Write(manifestPath, &manifest.Manifest{Version: 1, Project: manifest.ProjectRef{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}

	// Dry-run: preview only — no managed file may appear.
	var out bytes.Buffer
	if err := runApply(applyOptions{recipePath: recipePath, manifest: manifestPath, dryRun: true, cwd: dir, out: &out, err: &out}); err != nil {
		t.Fatalf("apply --dry-run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"Onboarding (Git)", "managed adoption via sealed plan", "pre-commit", "pre-merge-commit"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run preview missing %q:\n%s", want, out.String())
		}
	}
	assertNoOnboardingArtifacts(t, dir)

	// Real run: applies through the sealed plan.
	out.Reset()
	if err := runApply(applyOptions{recipePath: recipePath, manifest: manifestPath, cwd: dir, out: &out, err: &out}); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !bytes.Contains(b, []byte("# >>> orotools onboarding v1 >>>")) {
		t.Fatalf("managed ignore block missing after apply: %q %v", b, err)
	}
	for _, hook := range []string{"pre-commit", "pre-merge-commit"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", hook)); err != nil {
			t.Errorf("hook %s not installed: %v", hook, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", initStateFile)); err != nil {
		t.Errorf("ownership state missing: %v", err)
	}
}

// A conflicted repository must surface the block through `oro apply` without
// failing the command as a whole (recoverable step, like init).
func TestRunApply_OnboardingConflictReportsWithoutFailing(t *testing.T) {
	dir := onboardingRepo(t)
	onboardingGit(t, dir, "config", "--local", "core.hooksPath", ".hooks")

	recipeYAML := `version: 1
name: adopt-demo
description: onboarding adoption demo
steps:
  - id: git-onboarding
    type: onboarding
    protect_branches:
      - main
`
	recipePath := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(recipePath, []byte(recipeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, manifest.Filename)
	if err := manifest.Write(manifestPath, &manifest.Manifest{Version: 1, Project: manifest.ProjectRef{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runApply(applyOptions{recipePath: recipePath, manifest: manifestPath, cwd: dir, out: &out, err: &out}); err != nil {
		t.Fatalf("apply must not fail on blocked onboarding: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "core.hooksPath") || !strings.Contains(out.String(), "onboarding blocked") {
		t.Errorf("output missing conflict guidance:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Error("blocked onboarding wrote the ignore block")
	}
	if v := onboardingGit(t, dir, "config", "--local", "core.hooksPath"); strings.TrimSpace(v) != ".hooks" {
		t.Errorf("core.hooksPath changed: %q", v)
	}
}
