package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"oroborus.dev/orotools/internal/agent"
	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/executor"
	"oroborus.dev/orotools/internal/installer"
	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/pipeline"
	pipeproviders "oroborus.dev/orotools/internal/pipeline/providers"
	"oroborus.dev/orotools/internal/planner"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/repository"
	"oroborus.dev/orotools/internal/repository/providers"
	"oroborus.dev/orotools/internal/requirements"
	"oroborus.dev/orotools/internal/skill"
	"oroborus.dev/orotools/internal/ui"
	"oroborus.dev/orotools/internal/variables"
)

// newOptions carries parsed flags and streams for `oro new`.
type newOptions struct {
	name         string
	stack        string
	recipePath   string
	set          []string // raw "key=value"
	yes          bool
	dryRun       bool
	repository   string
	pipeline     string
	ai           string
	remote       string
	visibility   string
	organization string
	azureProject string
	repoRunner   repository.CommandRunner
	stdin        io.Reader
	out          io.Writer
	err          io.Writer
}

// runNew is the end-to-end flow for `oro new <name>` (spec sections 53-54, 65).
// Stages not yet built (git/repo/pipeline/skills/agents) are reported as
// skipped with their owning phase.
func runNew(opts newOptions) error {
	out := writerOrStdout(opts.out)
	status := ui.NewStatus(out)

	if opts.name == "" {
		return fmt.Errorf("new: nome do projeto é obrigatório (oro new <nome>)")
	}
	if opts.recipePath == "" && opts.stack == "" {
		return fmt.Errorf("new: --stack ou --recipe é obrigatório")
	}

	src, err := resolveRecipe(opts.stack, opts.recipePath)
	if err != nil {
		return err
	}
	r, err := loadRecipeFromSource(src, opts.recipePath)
	if err != nil {
		return fmt.Errorf("carregar recipe: %w", err)
	}

	provided, err := parseSetFlags(opts.set)
	if err != nil {
		return err
	}

	// Variable resolution: flag > wizard > default.
	var flagVars, wizardVars map[string]any
	flagVars = provided
	if !opts.yes {
		wizardVars, err = promptVariables(r.Variables, provided)
		if err != nil {
			return err
		}
	}
	res, err := variables.Resolve(r, opts.name, variables.Inputs{Flags: flagVars, Wizard: wizardVars})
	if err != nil {
		return fmt.Errorf("resolver variáveis: %w", err)
	}

	agentName := opts.ai
	if agentName == "" {
		agentName = "opencode"
	}

	status.Title(fmt.Sprintf("Orotools — %s", res.Project.Name))
	fmt.Fprintln(out)

	// Requirements (skip in dry-run — spec section 25: no changes executed).
	if !opts.dryRun {
		if err := checkRequirements(out, r, res); err != nil {
			return err
		}
	}

	// Plan.
	plan, err := planner.Build(r, res, conditions.Context{Vars: res.Vars}, agentName)
	if err != nil {
		return fmt.Errorf("plano: %w", err)
	}

	if opts.dryRun {
		fmt.Fprint(out, plan.DryRun())
		return nil
	}

	// Execute.
	projectBase := res.Project.Slug
	execCtx := executor.Context{
		Project: res.Project,
		Vars:    res.Vars,
		Base:    projectBase,
		Sources: src.FS,
		Stdout:  out,
		Stderr:  opts.err,
	}
	results := executor.Executor{}.Execute(plan, execCtx)
	reportExecution(out, results)
	if executor.HasFatal(results) {
		status.Error("um ou mais passos falharam; o projeto pode estar parcialmente criado")
		return fmt.Errorf("execução falhou com erros fatais")
	}

	recipeFS := src.FS

	if len(r.Skills.Bundled) > 0 {
		// Managed installer (skills-manager FR-21/T12): transactional install
		// with lock; only effectively installed skills are registered.
		svc := skill.Service{
			Base:         projectBase,
			Agent:        agentName,
			ManifestPath: filepath.Join(projectBase, manifest.Filename),
			LockPath:     filepath.Join(projectBase, skill.LockDir, skill.LockFilename),
		}
		cands, _, catErr := skill.RecipeCatalog{RecipeName: r.Name, FS: recipeFS, Refs: r.Skills.Bundled}.List(context.Background())
		if catErr != nil {
			status.Warn(fmt.Sprintf("skills bundled: %v", catErr))
		} else {
			lock := skill.NewLock()
			installed, failed := 0, 0
			for _, cand := range cands {
				if _, err := svc.Install(nil, lock, cand); err != nil {
					status.Warn(fmt.Sprintf("skill %s: %v", cand.Name, err))
					failed++
					continue
				}
				installed++
			}
			if failed > 0 {
				status.Warn(fmt.Sprintf("skills bundled: %d instalada(s), %d falharam; rode `oro skills` no projeto para concluir", installed, failed))
			} else {
				status.Success(fmt.Sprintf("skills bundled: %d instaladas", installed))
			}
		}
	}

	if len(r.Agents.Bundled) > 0 {
		if err := agent.InstallBundled(agent.BundledOptions{
			Base:     projectBase,
			Agent:    agentName,
			RecipeFS: recipeFS,
			Bundled:  r.Agents.Bundled,
			Stdout:   out,
			Stderr:   opts.err,
		}); err != nil {
			status.Warn(fmt.Sprintf("agents bundled: %v", err))
		} else {
			status.Success(fmt.Sprintf("agents bundled: %d instalados", len(r.Agents.Bundled)))
		}
	}

	pipeName := resolvePipeline(opts)
	if pipeName != "none" {
		p, providerErr := pipeproviders.Get(pipeName)
		if providerErr != nil {
			status.Warn(providerErr.Error())
		} else {
			pipeOpts := pipeline.Options{
				Base:       projectBase,
				RecipeName: r.Name,
				RecipeFS:   os.DirFS(filepath.Dir(opts.recipePath)),
				Vars:       res.Vars,
				Stdout:     out,
				Stderr:     opts.err,
			}
			if err := p.Generate(context.Background(), pipeline.Project(res.Project), pipeOpts); err != nil {
				status.Warn(fmt.Sprintf("pipeline: %v", err))
			} else {
				status.Success(fmt.Sprintf("pipeline: %s", pipeName))
			}
		}
	}

	if len(r.Skills.External) > 0 || len(r.Agents.External) > 0 {
		instOps, planErr := installer.PlanAll(r, agentName)
		if planErr != nil {
			status.Warn(planErr.Error())
		} else if err := installer.Run(context.Background(), instOps, installer.Options{
			Base:   projectBase,
			Recipe: r,
			Yes:    opts.yes,
			DryRun: false,
			Runner: opts.repoRunner,
			Stdout: out,
			Stderr: opts.err,
			Stdin:  readerOrStdin(opts.stdin),
		}); err != nil {
			status.Warn(fmt.Sprintf("instaladores externos: %v", err))
		} else {
			status.Success(fmt.Sprintf("instaladores externos: %d", len(instOps)))
		}
	}

	// Manifest (desired state).
	m := &manifest.Manifest{
		Version:    1,
		Recipe:     manifest.RecipeRef{Name: r.Name, Version: r.Version},
		Project:    manifest.ProjectRef{Name: res.Project.Name},
		Variables:  res.Vars,
		Repository: manifest.ProviderRef{Provider: repoProviderName(opts.repository)},
		Pipeline:   manifest.ProviderRef{Provider: pipeName},
		AI:         manifest.AIRef{Agent: agentName},
		Skills: manifest.SkillsRef{
			Bundled:  append([]string(nil), r.Skills.Bundled...),
			External: skill.FlattenExternal(r.Skills.External),
		},
		Agents: manifest.AgentsRef{
			Bundled:  append([]string(nil), r.Agents.Bundled...),
			External: agent.FlattenExternal(r.Agents.External),
		},
	}
	manifestPath := filepath.Join(projectBase, manifest.Filename)
	if err := manifest.Write(manifestPath, m); err != nil {
		return fmt.Errorf("escrever manifest: %w", err)
	}
	status.Success(fmt.Sprintf("manifest: %s", manifestPath))

	// Local Git is universal; remote provider is optional. Remote failures are
	// recoverable: local project and manifest remain available for retry.
	repoName := repoProviderName(opts.repository)
	localProvider, providerErr := providers.Get("local", opts.repoRunner)
	if providerErr == nil {
		if err := localProvider.Create(context.Background(), repository.Project(res.Project), repository.Options{Base: projectBase, Stdout: out, Stderr: opts.err}); err != nil {
			status.Error(fmt.Sprintf("git: %v", err))
		} else {
			status.Success("git inicializado")
		}
	}
	if repoName != "local" {
		remoteProvider, err := providers.Get(repoName, opts.repoRunner)
		if err != nil {
			status.Error(err.Error())
		} else {
			optsRepo := repository.Options{Base: projectBase, Remote: opts.remote, Visibility: opts.visibility, Organization: opts.organization, AzureProject: opts.azureProject, Stdout: out, Stderr: opts.err}
			if err := remoteProvider.Create(context.Background(), repository.Project(res.Project), optsRepo); err != nil {
				status.Warn(fmt.Sprintf("repositório remoto: %v", err))
			} else if err := remoteProvider.ConfigureRemote(context.Background(), repository.Project(res.Project), optsRepo); err != nil {
				status.Warn(fmt.Sprintf("configuração do remote: %v", err))
			} else {
				status.Success("repositório remoto configurado")
			}
		}
	}

	fmt.Fprintln(out)
	status.Success("Projeto pronto.")
	fmt.Fprintf(out, "cd %s\n", projectBase)
	return nil
}

func repoProviderName(repository string) string {
	if repository == "" {
		return "local"
	}
	return repository
}

func resolvePipeline(opts newOptions) string {
	if opts.pipeline != "" {
		return opts.pipeline
	}
	return pipeline.DefaultProvider(repoProviderName(opts.repository))
}

// parseSetFlags turns ["db=postgres", "docker=true"] into a map. Values keep
// their string form; the variables resolver coerces them per declared type.
func parseSetFlags(set []string) (map[string]any, error) {
	out := map[string]any{}
	for _, raw := range set {
		k, v, ok := strings.Cut(raw, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--set inválido %q (esperado chave=valor)", raw)
		}
		out[k] = v
	}
	return out, nil
}

// checkRequirements runs the requirements checker and aborts on any unsatisfied
// non-skipped requirement (spec section 31 — missing runtime is Fatal).
func checkRequirements(out io.Writer, r *recipe.Recipe, res *variables.Result) error {
	status := ui.NewStatus(out)
	results := requirements.Checker{}.Check(r.Requirements, conditions.Context{Vars: res.Vars})
	failed := 0
	for _, rr := range results {
		switch {
		case rr.Skipped:
			status.Info(fmt.Sprintf("%s (pulando)", rr.Requirement.Command))
		case rr.Satisfied && rr.FoundVersion != "":
			status.Success(fmt.Sprintf("%s %s", rr.Requirement.Command, rr.FoundVersion))
		case rr.Satisfied:
			status.Success(rr.Requirement.Command)
		default:
			status.Error(fmt.Sprintf("%s — %s", rr.Requirement.Command, rr.Reason))
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d requisito(s) não satisfeito(s) (rode `oro doctor --recipe %s` para detalhes)", failed, "<recipe>")
	}
	return nil
}

// reportExecution prints a one-line status per executed step.
func reportExecution(out io.Writer, results []executor.Result) {
	status := ui.NewStatus(out)
	for _, r := range results {
		label := r.Step.ID
		if label == "" {
			label = fmt.Sprintf("%s %s", r.Step.Type, r.Step.Command)
		}
		switch {
		case r.Error != nil:
			status.Error(fmt.Sprintf("%s: %v", label, r.Error))
		case r.Action == "skipped":
			status.Info(fmt.Sprintf("%s (pulando)", label))
		default:
			status.Success(label)
		}
	}
}
