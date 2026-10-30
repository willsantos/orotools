package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/planner"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

// onboardingRepo creates a throwaway Git repository (hermetic: local git only).
func onboardingRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	c := exec.Command("git", "-C", d, "init")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git init: %s %v", b, e)
	}
	return d
}

// planOnboarding builds a real plan (planner preflight embeds the sealed
// onboarding plan) for a single onboarding step against dir.
func planOnboarding(t *testing.T, dir string, protect bool) *planner.Plan {
	t.Helper()
	s := recipe.Step{ID: "adopt", Type: "onboarding"}
	if protect {
		s.ProtectBranches = []string{"main"}
	}
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{s}}
	res := &variables.Result{Project: variables.Project{Name: "p", Slug: "p"}, Vars: map[string]any{}}
	p, err := planner.Build(r, res, conditions.Context{Vars: res.Vars, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("planner.Build: %v", err)
	}
	return p
}

func TestOnboarding_ExecutorAppliesThroughSealedPlan(t *testing.T) {
	dir := onboardingRepo(t)
	plan := planOnboarding(t, dir, true)
	ctx := Context{Base: dir, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}

	results := Executor{}.Execute(plan, ctx)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	if r.Error != nil || r.Action != ActRan {
		t.Fatalf("onboarding result = %+v, want clean run", r)
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), "# >>> orotools onboarding v1 >>>") {
		t.Fatalf("managed ignore block missing: %q %v", ignore, err)
	}
	for _, hook := range []string{"pre-commit", "pre-merge-commit"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", hook)); err != nil {
			t.Errorf("hook %s not installed: %v", hook, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "orotools-onboarding-state-v1.json")); err != nil {
		t.Errorf("ownership state missing: %v", err)
	}

	// Re-running the same sealed plan is a no-op (idempotent adoption).
	results = Executor{}.Execute(plan, ctx)
	if results[0].Error != nil || results[0].Action != ActRan {
		t.Fatalf("second run = %+v, want idempotent success", results[0])
	}
}

func TestOnboarding_TamperedSealedPlanIsRefused(t *testing.T) {
	dir := onboardingRepo(t)
	plan := planOnboarding(t, dir, false)
	// Simulate in-process tampering of the sealed plan: the HMAC seal no longer
	// matches and onboarding.Apply must refuse without writing anything.
	plan.Onboarding[0].Sealed.IgnorePath = filepath.Join(t.TempDir(), "forged-ignore")

	ctx := Context{Base: dir, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}
	results := Executor{}.Execute(plan, ctx)
	r := results[0]
	if r.Error == nil {
		t.Fatal("tampered plan must be refused")
	}
	if !strings.Contains(r.Error.Error(), "onboarding plan") && !strings.Contains(r.Error.Error(), "inspect again") {
		t.Errorf("unexpected refusal reason: %v", r.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Error("tampered plan produced writes")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "orotools-onboarding-state-v1.json")); !os.IsNotExist(err) {
		t.Error("tampered plan wrote ownership state")
	}
}

func TestOnboarding_BlockedOpReportsAndRecipeContinues(t *testing.T) {
	dir := onboardingRepo(t)
	c := exec.Command("git", "-C", dir, "config", "--local", "core.hooksPath", ".hooks")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git config: %s %v", b, e)
	}
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{
		{ID: "adopt", Type: "onboarding", ProtectBranches: []string{"main"}},
		{ID: "after", Type: "mkdir", Path: "still-created"},
	}}
	res := &variables.Result{Project: variables.Project{Name: "p", Slug: "p"}, Vars: map[string]any{}}
	plan, err := planner.Build(r, res, conditions.Context{Vars: res.Vars, Base: dir}, "opencode")
	if err != nil {
		t.Fatalf("planner.Build: %v", err)
	}
	ctx := Context{Base: dir, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}
	results := Executor{}.Execute(plan, ctx)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	blocked := results[0]
	if blocked.Error == nil || blocked.Severity != SevRecoverable {
		t.Fatalf("blocked onboarding = %+v, want recoverable diagnostic", blocked)
	}
	if !strings.Contains(blocked.Error.Error(), "core.hooksPath") {
		t.Errorf("diagnostic missing conflict guidance: %v", blocked.Error)
	}
	if results[1].Action != ActCreated {
		t.Errorf("remaining step must still run: %+v", results[1])
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Error("blocked onboarding wrote the ignore block")
	}
	if HasFatal(results) {
		t.Error("blocked onboarding must not be fatal")
	}
}

func TestOnboarding_InspectFailureIsRecoverableBlock(t *testing.T) {
	dir := t.TempDir() // not a Git repository
	plan := planOnboarding(t, dir, false)
	ctx := Context{Base: dir, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}
	results := Executor{}.Execute(plan, ctx)
	if results[0].Error == nil || results[0].Severity != SevRecoverable {
		t.Fatalf("result = %+v, want recoverable block", results[0])
	}
	if !strings.Contains(results[0].Error.Error(), "not a Git worktree") {
		t.Errorf("diagnostic = %v, want not-a-worktree guidance", results[0].Error)
	}
}

func TestOnboarding_MissingPlannedOpIsFatal(t *testing.T) {
	dir := onboardingRepo(t)
	plan := buildPlan(recipe.Step{ID: "adopt", Type: "onboarding"})
	ctx := Context{Base: dir, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}
	results := Executor{}.Execute(plan, ctx)
	if results[0].Error == nil || results[0].Severity != SevFatal {
		t.Fatalf("result = %+v, want fatal missing-op error", results[0])
	}
}

func TestOnboarding_RootMismatchIsFatal(t *testing.T) {
	repo := onboardingRepo(t)
	other := onboardingRepo(t)
	plan := planOnboarding(t, repo, false)
	ctx := Context{Base: other, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}
	results := Executor{}.Execute(plan, ctx)
	if results[0].Error == nil || results[0].Severity != SevFatal {
		t.Fatalf("result = %+v, want fatal root mismatch", results[0])
	}
	if !strings.Contains(results[0].Error.Error(), "rebuild the plan") {
		t.Errorf("diagnostic = %v", results[0].Error)
	}
	if _, err := os.Stat(filepath.Join(other, ".gitignore")); !os.IsNotExist(err) {
		t.Error("plan leaked into a different base")
	}
}
