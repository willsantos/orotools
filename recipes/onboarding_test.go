package recipes

import (
	"os/exec"
	"testing"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/planner"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

// TestRailsOnboardingStepIsOptIn demonstrates the recipe-level onboarding
// wiring (M04.3/M04.6): the rails recipe declares an `onboarding` step behind
// an `onboarding` boolean variable whose default is false, so plan behavior is
// unchanged unless the user explicitly opts in with --set onboarding=true.
func TestRailsOnboardingStepIsOptIn(t *testing.T) {
	fsys, err := Open("rails")
	if err != nil {
		t.Fatal(err)
	}
	r, err := recipe.Load(fsys, RecipeFile)
	if err != nil {
		t.Fatalf("load rails recipe: %v", err)
	}
	if errs := recipe.Validate(r); len(errs) != 0 {
		t.Fatalf("rails recipe invalid: %v", errs)
	}

	// Default resolution: the step is filtered out before any onboarding
	// inspection would run — the base here is not even a Git repository.
	res, err := variables.Resolve(r, "demo", variables.Inputs{})
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	nonGit := t.TempDir()
	p, err := planner.Build(r, res, conditions.Context{Vars: res.Vars, Base: nonGit}, "opencode")
	if err != nil {
		t.Fatalf("plan with defaults: %v", err)
	}
	if len(p.Onboarding) != 0 {
		t.Fatalf("onboarding must be opt-in; got default-plan ops %+v", p.Onboarding)
	}

	// Opt-in via the variable: the planner runs the read-only preflight against
	// a throwaway Git repository and embeds the sealed plan.
	repo := t.TempDir()
	c := exec.Command("git", "-C", repo, "init")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git init: %s %v", b, e)
	}
	res, err = variables.Resolve(r, "demo", variables.Inputs{Flags: map[string]any{"onboarding": true}})
	if err != nil {
		t.Fatalf("resolve opt-in: %v", err)
	}
	p, err = planner.Build(r, res, conditions.Context{Vars: res.Vars, Base: repo}, "opencode")
	if err != nil {
		t.Fatalf("plan with onboarding=true: %v", err)
	}
	if len(p.Onboarding) != 1 {
		t.Fatalf("opt-in plan onboarding ops = %d, want 1", len(p.Onboarding))
	}
	op := p.Onboarding[0]
	if op.StepID != "git-onboarding" || op.Blocked {
		t.Fatalf("op = %+v, want unblocked git-onboarding", op)
	}
	if len(op.ProtectBranches) != 2 {
		t.Errorf("ProtectBranches = %v, want main+develop", op.ProtectBranches)
	}
}
