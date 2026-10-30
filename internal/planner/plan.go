package planner

import (
	"fmt"
	"path/filepath"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/installer"
	"oroborus.dev/orotools/internal/onboarding"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

// Category labels the kind of operation a planned step represents.
type Category int

const (
	CatDirectory Category = iota
	CatFile
	CatCommand
	CatMessage
	CatOnboarding
)

// PlannedStep is a recipe step that survived the when-filter, annotated with
// its category and (for commands) network flag.
type PlannedStep struct {
	recipe.Step
	Category Category
	Network  bool
}

// FileOp describes a copy or template file operation for rendering.
type FileOp struct {
	Kind        string // "copy" | "template"
	Source      string
	Destination string
}

// CommandOp describes an exec operation for rendering.
type CommandOp struct {
	Command          string
	Args             []string
	WorkingDirectory string
	Network          bool
}

// ExternalInstallerOp describes an external installer for dry-run rendering.
type ExternalInstallerOp struct {
	Name    string
	Label   string
	Command string
	Args    []string
}

// OnboardingOp is the planned Git onboarding adoption declared by an
// `onboarding` recipe step. Planning is read-only: it invokes
// onboarding.PlanApply for the plan base and embeds the resulting sealed plan.
// The executor applies exclusively through Sealed (onboarding.Apply verifies
// the seal and re-inspects); nothing is written by inspection alone. When
// Blocked is true, Sealed must not be applied and Conflicts carry the
// diagnostics.
type OnboardingOp struct {
	StepID          string
	Root            string
	ProtectBranches []string
	Actions         []string
	Conflicts       []string
	Files           []string
	Blocked         bool
	Sealed          onboarding.Plan
}

// Plan is the resolved, filtered view of a recipe ready for execution or
// dry-run rendering.
type Plan struct {
	Project            variables.Project
	Steps              []PlannedStep
	Directories        []string
	Files              []FileOp
	Commands           []CommandOp
	Messages           []string
	BundledSkills      []string
	BundledAgents      []string
	ExternalInstallers []ExternalInstallerOp
	Onboarding         []OnboardingOp
	LocalOps           int
	NetworkOps         int
	NetworkRequired    bool
}

// Build constructs the execution plan for r given resolved variables. Steps
// whose when evaluates false are excluded. Steps are kept in declared order.
// agentID is the canonical AI agent target used for external agent installer aliases.
func Build(r *recipe.Recipe, res *variables.Result, ctx conditions.Context, agentID string) (*Plan, error) {
	if r == nil || res == nil {
		return nil, fmt.Errorf("planner: recipe and result are required")
	}
	p := &Plan{Project: res.Project}

	for i := range r.Steps {
		s := r.Steps[i]
		if s.When != nil {
			ok, err := conditions.Evaluate(*s.When, ctx)
			if err != nil {
				return nil, fmt.Errorf("step %q when: %w", s.ID, err)
			}
			if !ok {
				continue
			}
		}
		if err := addToPlan(p, s, ctx); err != nil {
			return nil, err
		}
	}
	p.BundledSkills = append([]string(nil), r.Skills.Bundled...)
	p.BundledAgents = append([]string(nil), r.Agents.Bundled...)
	if err := appendExternalInstallers(p, r, agentID); err != nil {
		return nil, err
	}
	p.NetworkRequired = p.NetworkOps > 0
	return p, nil
}

func appendExternalInstallers(p *Plan, r *recipe.Recipe, agentID string) error {
	ops, err := installer.PlanAll(r, agentID)
	if err != nil {
		return err
	}
	for _, op := range ops {
		p.ExternalInstallers = append(p.ExternalInstallers, ExternalInstallerOp{
			Name:    op.InstallerName,
			Label:   op.Label,
			Command: op.Command,
			Args:    append([]string(nil), op.Args...),
		})
		p.NetworkOps++
	}
	return nil
}

func addToPlan(p *Plan, s recipe.Step, ctx conditions.Context) error {
	cat, err := categorize(s.Type)
	if err != nil {
		return fmt.Errorf("step %q: %w", s.ID, err)
	}
	ps := PlannedStep{Step: s, Category: cat}

	switch cat {
	case CatDirectory:
		p.Directories = append(p.Directories, s.Path)
		p.LocalOps++
	case CatFile:
		p.Files = append(p.Files, FileOp{Kind: s.Type, Source: s.Source, Destination: s.Destination})
		p.LocalOps++
	case CatCommand:
		net := isNetwork(s.Command)
		p.Commands = append(p.Commands, CommandOp{
			Command:          s.Command,
			Args:             append([]string(nil), s.Args...),
			WorkingDirectory: s.WorkingDirectory,
			Network:          net,
		})
		ps.Network = net
		if net {
			p.NetworkOps++
		} else {
			p.LocalOps++
		}
	case CatMessage:
		p.Messages = append(p.Messages, s.Text)
	case CatOnboarding:
		p.Onboarding = append(p.Onboarding, planOnboarding(ctx, s))
		p.LocalOps++
	}

	p.Steps = append(p.Steps, ps)
	return nil
}

// planOnboarding runs the read-only onboarding preflight for s against the
// plan base (empty base means the current directory, matching executor
// semantics for `oro apply`). Inspection failures and conflicts are recorded
// on the op as blocks — never as plan-level errors — so the preview can list
// them and the executor refuses to apply that specific action while the rest
// of the recipe continues.
func planOnboarding(ctx conditions.Context, s recipe.Step) OnboardingOp {
	base := ctx.Base
	if base == "" {
		base = "."
	}
	op := OnboardingOp{StepID: s.ID, ProtectBranches: append([]string(nil), s.ProtectBranches...)}
	opts := onboarding.Options{}
	if len(s.ProtectBranches) > 0 {
		opts = onboarding.Options{EnableProtection: true, ProtectedBranches: op.ProtectBranches}
	}
	p, err := onboarding.PlanApply(base, opts)
	if err != nil {
		op.Blocked = true
		op.Conflicts = []string{err.Error()}
		return op
	}
	op.Root = p.Root
	op.Actions = append([]string(nil), p.Actions...)
	op.Conflicts = append([]string(nil), p.Conflicts...)
	op.Files = []string{p.IgnorePath, p.StatePath}
	if opts.EnableProtection {
		op.Files = append(op.Files, filepath.Join(p.HooksDir, "pre-commit"), filepath.Join(p.HooksDir, "pre-merge-commit"))
	}
	op.Blocked = len(p.Conflicts) > 0
	op.Sealed = p
	return op
}

func categorize(typ string) (Category, error) {
	switch typ {
	case "mkdir":
		return CatDirectory, nil
	case "copy", "template":
		return CatFile, nil
	case "exec":
		return CatCommand, nil
	case "message":
		return CatMessage, nil
	case "onboarding":
		return CatOnboarding, nil
	default:
		return 0, fmt.Errorf("unknown step type %q", typ)
	}
}
