package installer_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/installer"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/repository"
)

type fakeRunner struct{ calls []string }

func (f *fakeRunner) LookPath(string) (string, error) { return "npx", nil }
func (f *fakeRunner) Run(_ context.Context, name string, args []string, _ string, _, _ io.Writer) error {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return nil
}

func TestPlanNpxInstallers(t *testing.T) {
	r := &recipe.Recipe{
		Skills: recipe.Skills{
			External: []recipe.ExternalSkill{
				{Installer: "impeccable"},
				{Installer: "tech-leads-club", Skills: []string{"tlc-spec-driven"}},
			},
		},
		ExternalInstallers: map[string]recipe.ExternalInstaller{
			"impeccable": {
				Runtime: "npx",
				Package: recipe.ExternalInstallerPackage{Name: "impeccable", Version: "latest"},
				Install: recipe.ExternalInstallerOp{Args: []string{"install", "--scope=project"}},
			},
			"tech-leads-club": {
				Runtime: "npx",
				Package: recipe.ExternalInstallerPackage{Name: "@tech-leads-club/agent-skills", Version: "latest"},
			},
		},
	}
	ops, err := installer.Plan(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("ops = %d", len(ops))
	}
	if ops[0].Command != "npx" || !strings.Contains(strings.Join(ops[0].Args, " "), "install --scope=project") {
		t.Errorf("impeccable op = %+v", ops[0])
	}
	if !strings.Contains(strings.Join(ops[1].Args, " "), "tlc-spec-driven") {
		t.Errorf("tech-leads-club op = %+v", ops[1])
	}
}

func TestRunWithYes(t *testing.T) {
	f := &fakeRunner{}
	r := &recipe.Recipe{
		Skills: recipe.Skills{External: []recipe.ExternalSkill{{Installer: "impeccable"}}},
		ExternalInstallers: map[string]recipe.ExternalInstaller{
			"impeccable": {
				Runtime: "npx",
				Package: recipe.ExternalInstallerPackage{Name: "impeccable"},
				Install: recipe.ExternalInstallerOp{Args: []string{"install"}},
			},
		},
	}
	ops, err := installer.Plan(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.Run(context.Background(), ops, installer.Options{
		Base:   ".",
		Recipe: r,
		Yes:    true,
		Runner: f,
	}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestPlanAgentsExternalUsesAlias(t *testing.T) {
	r := &recipe.Recipe{
		Agents: recipe.Agents{
			External: []recipe.ExternalAgent{{Installer: "tech-leads-club"}},
		},
		ExternalInstallers: map[string]recipe.ExternalInstaller{
			"tech-leads-club": {
				Runtime: "npx",
				Package: recipe.ExternalInstallerPackage{Name: "@tech-leads-club/agent-skills"},
			},
		},
	}
	ops, err := installer.PlanAll(r, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %d", len(ops))
	}
	if !strings.Contains(strings.Join(ops[0].Args, " "), "claude-code") {
		t.Errorf("expected alias in args: %v", ops[0].Args)
	}
}

func TestRunDeclinedPrompt(t *testing.T) {
	r := &recipe.Recipe{
		Skills: recipe.Skills{External: []recipe.ExternalSkill{{Installer: "impeccable"}}},
		ExternalInstallers: map[string]recipe.ExternalInstaller{
			"impeccable": {Runtime: "npx", Package: recipe.ExternalInstallerPackage{Name: "impeccable"}},
		},
	}
	ops, _ := installer.Plan(r)
	err := installer.Run(context.Background(), ops, installer.Options{
		Recipe: r,
		Stdin:  strings.NewReader("n\n"),
		Stdout: io.Discard,
		Runner: repository.RealRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Errorf("expected declined error, got %v", err)
	}
}

func TestRunDryRunSkipsExecution(t *testing.T) {
	f := &fakeRunner{}
	r := &recipe.Recipe{
		Skills: recipe.Skills{External: []recipe.ExternalSkill{{Installer: "impeccable"}}},
		ExternalInstallers: map[string]recipe.ExternalInstaller{
			"impeccable": {Runtime: "npx", Package: recipe.ExternalInstallerPackage{Name: "impeccable"}},
		},
	}
	ops, _ := installer.Plan(r)
	if err := installer.Run(context.Background(), ops, installer.Options{Recipe: r, DryRun: true, Runner: f}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("dry-run should not execute, calls=%v", f.calls)
	}
}
