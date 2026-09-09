package cli

import (
	"fmt"
	"io"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/executor"
	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/planner"
	"oroborus.dev/orotools/internal/ui"
	"oroborus.dev/orotools/internal/variables"
)

// applyOptions carries parsed flags and streams for `oro apply`.
type applyOptions struct {
	stack      string
	recipePath string
	manifest   string
	dryRun     bool
	out        io.Writer
	err        io.Writer
}

// runApply reconciles the current project with orotools.yaml + recipe (spec
// section 56). Re-runs the plan; executor idempotency makes already-satisfied
// steps no-ops (spec section 28).
func runApply(opts applyOptions) error {
	out := writerOrStdout(opts.out)
	status := ui.NewStatus(out)

	m, err := manifest.Read(opts.manifest)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	stack := opts.stack
	if stack == "" && opts.recipePath == "" && m.Recipe.Name != "" {
		stack = m.Recipe.Name
	}
	if opts.recipePath == "" && stack == "" {
		return fmt.Errorf("apply: --stack ou --recipe é obrigatório")
	}

	src, err := resolveRecipe(stack, opts.recipePath)
	if err != nil {
		return err
	}
	r, err := loadRecipeFromSource(src, opts.recipePath)
	if err != nil {
		return fmt.Errorf("load recipe: %w", err)
	}

	res, err := variables.Resolve(r, m.Project.Name, variables.Inputs{Flags: m.Variables})
	if err != nil {
		return fmt.Errorf("resolve variables: %w", err)
	}

	status.Title(fmt.Sprintf("Orotools apply — %s", m.Project.Name))
	fmt.Fprintln(out)

	agentID := m.AI.Agent
	if agentID == "" {
		agentID = "opencode"
	}
	plan, err := planner.Build(r, res, conditions.Context{Vars: res.Vars}, agentID)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	if opts.dryRun {
		fmt.Fprint(out, plan.DryRun())
		return nil
	}

	execCtx := executor.Context{
		Project: res.Project,
		Vars:    res.Vars,
		Base:    ".",
		Sources: src.FS,
		Stdout:  out,
		Stderr:  opts.err,
	}
	results := executor.Executor{}.Execute(plan, execCtx)
	reportExecution(out, results)

	fmt.Fprintln(out)
	if executor.HasFatal(results) {
		status.Error("um ou mais passos falharam; o projeto pode estar parcialmente aplicado")
		return fmt.Errorf("apply concluído com erros fatais")
	}
	// FR-21 (skills-manager): apply never updates installed skills.
	if len(m.Skills.Bundled) > 0 || len(m.Skills.Managed) > 0 {
		status.Info("skills não são atualizadas pelo apply; use `oro skills update`")
	}
	status.Success("apply concluído")
	return nil
}
