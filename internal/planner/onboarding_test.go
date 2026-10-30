package planner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/recipe"
)

// onboardingRepo creates a throwaway Git repository for hermetic planner
// preflights (onboarding.Inspect shells out to the local git binary; no
// network is involved).
func onboardingRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	c := exec.Command("git", "-C", d, "init")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git init: %s %v", b, e)
	}
	return d
}

func onboardingRecipe(branch bool) *recipe.Recipe {
	s := recipe.Step{ID: "adopt", Type: "onboarding"}
	if branch {
		s.ProtectBranches = []string{"main", "develop"}
	}
	return &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{s, {ID: "hi", Type: "message", Text: "done"}}}
}

func TestBuild_OnboardingStepEmbedsSealedPlan(t *testing.T) {
	dir := onboardingRepo(t)
	p, err := Build(onboardingRecipe(false), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Onboarding) != 1 {
		t.Fatalf("Onboarding ops = %d, want 1", len(p.Onboarding))
	}
	op := p.Onboarding[0]
	if op.StepID != "adopt" || op.Blocked {
		t.Fatalf("op = %+v, want unblocked op for step adopt", op)
	}
	if op.Root == "" || op.Root != filepath.Clean(dir) && op.Root != mustAbs(t, dir) {
		t.Errorf("op.Root = %q, want the repo root", op.Root)
	}
	// Without protect_branches only ignore + state are managed; the sealed plan
	// must carry no hook action.
	for _, a := range op.Actions {
		if strings.Contains(a, "hook") {
			t.Errorf("unexpected hook action without protection opt-in: %q", a)
		}
	}
	if len(op.Files) != 2 {
		t.Errorf("Files = %v, want ignore + state only", op.Files)
	}
	if len(op.Conflicts) != 0 {
		t.Errorf("Conflicts = %v, want none", op.Conflicts)
	}
	if op.Sealed.Root != op.Root || op.Sealed.IgnorePath == "" || op.Sealed.StatePath == "" {
		t.Errorf("sealed plan incomplete: %+v", op.Sealed)
	}
	// Onboarding counts as a local operation; message steps do not.
	if p.LocalOps != 1 {
		t.Errorf("LocalOps = %d, want 1 (onboarding)", p.LocalOps)
	}
}

func TestBuild_OnboardingProtectionOptIn(t *testing.T) {
	dir := onboardingRepo(t)
	p, err := Build(onboardingRecipe(true), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := p.Onboarding[0]
	if len(op.ProtectBranches) != 2 {
		t.Fatalf("ProtectBranches = %v, want main+develop", op.ProtectBranches)
	}
	joined := strings.Join(op.Actions, "; ")
	if !strings.Contains(joined, "pre-commit") {
		t.Errorf("Actions = %q, want branch protection hook action", joined)
	}
	if len(op.Files) != 4 {
		t.Errorf("Files = %v, want ignore + state + two hooks", op.Files)
	}
}

func TestBuild_OnboardingConflictsBlockTheOpNotThePlan(t *testing.T) {
	dir := onboardingRepo(t)
	c := exec.Command("git", "-C", dir, "config", "--local", "core.hooksPath", ".hooks")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git config: %s %v", b, e)
	}
	p, err := Build(onboardingRecipe(true), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build must not fail on onboarding conflicts: %v", err)
	}
	op := p.Onboarding[0]
	if !op.Blocked {
		t.Fatal("conflict must block the onboarding op")
	}
	if len(op.Conflicts) == 0 || !strings.Contains(strings.Join(op.Conflicts, "; "), "core.hooksPath") {
		t.Errorf("Conflicts = %v, want core.hooksPath diagnostic", op.Conflicts)
	}
	// The rest of the recipe is unaffected.
	if len(p.Messages) != 1 {
		t.Errorf("recipe steps beyond onboarding lost: %+v", p)
	}
	out := p.DryRun()
	if !strings.Contains(out, "blocked") || !strings.Contains(out, "core.hooksPath") {
		t.Errorf("dry-run must list the blocked op and conflict:\n%s", out)
	}
}

func TestBuild_OnboardingInspectFailureIsRecordedNotFatal(t *testing.T) {
	dir := t.TempDir() // not a Git repository
	p, err := Build(onboardingRecipe(false), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := p.Onboarding[0]
	if !op.Blocked || len(op.Conflicts) == 0 || !strings.Contains(op.Conflicts[0], "not a Git worktree") {
		t.Fatalf("op = %+v, want blocked with not-a-worktree diagnostic", op)
	}
	if !strings.Contains(p.DryRun(), "not a Git worktree") {
		t.Errorf("dry-run must surface the inspection failure:\n%s", p.DryRun())
	}
}

func TestBuild_OnboardingWhenFalseNeverInspects(t *testing.T) {
	// A non-git base would make any inspection produce a blocked op; a filtered
	// step must produce none at all.
	dir := t.TempDir()
	r := onboardingRecipe(false)
	yes := any(true)
	r.Steps[0].When = &recipe.Condition{Variable: "onboarding", Equals: &yes}
	p, err := Build(r, resWith("p", map[string]any{"onboarding": false}), conditions.Context{Vars: map[string]any{"onboarding": false}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Onboarding) != 0 || len(p.Steps) != 1 {
		t.Fatalf("filtered onboarding must vanish from the plan: %+v", p)
	}
}

func TestBuild_OnboardingRecipeWithoutOnboardingStepsIsPure(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{{ID: "d", Type: "mkdir", Path: "src"}}}
	p, err := Build(r, resWith("p", nil), conditions.Context{}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Onboarding) != 0 {
		t.Errorf("Onboarding = %+v, want empty", p.Onboarding)
	}
}

func TestDryRun_OnboardingSectionListsFilesActionsConflicts(t *testing.T) {
	dir := onboardingRepo(t)
	p, err := Build(onboardingRecipe(true), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := p.DryRun()
	for _, want := range []string{
		"Onboarding (Git)",
		"(managed adoption via sealed plan)",
		".gitignore",
		"orotools-onboarding-state-v1.json",
		"pre-commit",
		"pre-merge-commit",
		"append managed .gitignore block",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run missing %q:\n%s", want, out)
		}
	}
}

func TestDryRun_OnboardingBlockedTargetFallsBackToStepID(t *testing.T) {
	dir := t.TempDir()
	p, err := Build(onboardingRecipe(false), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(p.DryRun(), "step adopt (blocked)") {
		t.Errorf("blocked op without resolved root must name the step:\n%s", p.DryRun())
	}
}

// Guard the sealed-plan discipline: the plan summary the preview shows must be
// byte-identical to what the executor later reads — a mutated sealed plan is
// detected by onboarding.Apply, exercised in the executor tests.
func TestOnboardingOpSealedPlanSurvivesCopy(t *testing.T) {
	dir := onboardingRepo(t)
	p, err := Build(onboardingRecipe(false), resWith("p", nil), conditions.Context{Vars: map[string]any{}, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := p.Onboarding[0]
	if _, err := os.Stat(op.Sealed.IgnorePath); !os.IsNotExist(err) {
		t.Fatalf("planning wrote %s", op.Sealed.IgnorePath)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
