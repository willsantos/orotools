package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"oroborus.dev/orotools/internal/onboarding"
)

type onboardingCLIOptions struct {
	path, branches  string
	branchesChanged bool
	yes, dryRun     bool
	out             io.Writer
}

func onboardingOptions(raw string) (onboarding.Options, error) {
	if strings.TrimSpace(raw) == "" {
		return onboarding.Options{}, nil
	}
	var branches []string
	for _, v := range strings.Split(raw, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			return onboarding.Options{}, fmt.Errorf("--protect-branches contains an empty branch name")
		}
		branches = append(branches, v)
	}
	return onboarding.Options{EnableProtection: true, ProtectedBranches: branches}, nil
}

func validateProtectionFlag(raw string, changed bool) error {
	if changed && strings.TrimSpace(raw) == "" {
		return fmt.Errorf("--protect-branches was explicitly set but is empty; provide branch names or omit the flag")
	}
	return nil
}

func runOnboardingPlan(opts onboardingCLIOptions) error {
	root := opts.path
	if root == "" {
		root = "."
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	if e := validateProtectionFlag(opts.branches, opts.branchesChanged); e != nil {
		return e
	}
	o, e := onboardingOptions(opts.branches)
	if e != nil {
		return e
	}
	p, e := onboarding.PlanApply(abs, o)
	if e != nil {
		return e
	}
	printOnboardingPreview(opts.out, p, o)
	if len(p.Conflicts) > 0 {
		return fmt.Errorf("onboarding preview found %d conflict(s); no files changed", len(p.Conflicts))
	}
	return nil
}

func runOnboardingApply(opts onboardingCLIOptions) error {
	if opts.yes && opts.dryRun {
		return fmt.Errorf("--yes and --dry-run cannot be combined")
	}
	root := opts.path
	if root == "" {
		root = "."
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	if e := validateProtectionFlag(opts.branches, opts.branchesChanged); e != nil {
		return e
	}
	o, e := onboardingOptions(opts.branches)
	if e != nil {
		return e
	}
	p, e := onboarding.PlanApply(abs, o)
	if e != nil {
		return e
	}
	printOnboardingPreview(opts.out, p, o)
	if len(p.Conflicts) > 0 {
		return fmt.Errorf("onboarding blocked by %d conflict(s); no files changed", len(p.Conflicts))
	}
	if opts.dryRun {
		return nil
	}
	if !opts.yes {
		return fmt.Errorf("preview only; pass --yes to apply these exact onboarding changes")
	}
	if e = onboarding.Apply(p); e != nil {
		return e
	}
	fmt.Fprintln(opts.out, "Onboarding applied.")
	return nil
}

func runOnboardingUninstall(opts onboardingCLIOptions) error {
	root := opts.path
	if root == "" {
		root = "."
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	p, e := onboarding.PlanUninstall(abs)
	if e != nil {
		return e
	}
	fmt.Fprintln(opts.out, "OroTools onboarding uninstall — preview")
	fmt.Fprintf(opts.out, "Project: %s\n", p.Root)
	fmt.Fprintf(opts.out, "Candidate .gitignore: %s\n", p.IgnorePath)
	fmt.Fprintf(opts.out, "Ownership state: %s (remove after successful uninstall)\n", p.StatePath)
	fmt.Fprintf(opts.out, "Candidate hooks (only if owned and intact):\n  %s\n  %s\n", filepath.Join(p.HooksDir, "pre-commit"), filepath.Join(p.HooksDir, "pre-merge-commit"))
	fmt.Fprintln(opts.out, "Only exact owned content with matching state and modes can be removed; drift blocks removal.")
	printOnboardingConflicts(opts.out, p.Conflicts)
	if len(p.Conflicts) > 0 {
		return fmt.Errorf("uninstall blocked by %d conflict(s); no files changed", len(p.Conflicts))
	}
	if !opts.yes {
		return fmt.Errorf("preview only; pass --yes to uninstall intact managed onboarding")
	}
	if e = onboarding.Uninstall(p); e != nil {
		return e
	}
	fmt.Fprintln(opts.out, "Managed onboarding removed; original .gitignore restored.")
	return nil
}

func printOnboardingPreview(out io.Writer, p onboarding.Plan, o onboarding.Options) {
	fmt.Fprintln(out, "OroTools onboarding — read-only preview")
	fmt.Fprintf(out, "Project: %s\nGit dir: %s\nCommon dir: %s\n.gitignore: %s\n", p.Root, p.GitDir, p.CommonDir, p.IgnorePath)
	fmt.Fprintln(out, "Planned files:")
	fmt.Fprintf(out, "  .gitignore: %s\n", p.IgnorePath)
	fmt.Fprintf(out, "  ownership state: %s (create/update)\n", p.StatePath)
	fmt.Fprintln(out, "  existing bytes/mode preserved; append exact managed block")
	if o.EnableProtection {
		fmt.Fprintf(out, "  pre-commit: %s\n  pre-merge-commit: %s\n", filepath.Join(p.HooksDir, "pre-commit"), filepath.Join(p.HooksDir, "pre-merge-commit"))
		fmt.Fprintf(out, "Protected branches (local only): %s\n", strings.Join(o.ProtectedBranches, ", "))
	} else {
		fmt.Fprintln(out, "  hooks: none (branch protection not opted in)")
	}
	fmt.Fprintln(out, "Git limits: --no-verify bypasses hooks; fast-forward merges skip pre-merge-commit; remote policy/CI remains authoritative.")
	printOnboardingConflicts(out, p.Conflicts)
}
func printOnboardingConflicts(out io.Writer, conflicts []string) {
	if len(conflicts) == 0 {
		fmt.Fprintln(out, "Conflicts: none")
		return
	}
	fmt.Fprintln(out, "Conflicts:")
	for _, c := range conflicts {
		fmt.Fprintf(out, "  - %s\n", c)
	}
}
