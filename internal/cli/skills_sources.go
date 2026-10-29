package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

type sourceCommandOptions struct {
	manifestPath     string
	manifestExplicit bool
	agentFlag        string
	localRoots       []string
	id               string
	yes              bool
	dryRun           bool
	out              io.Writer
	loadGitHub       func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error)
	recipeCatalog    skill.CatalogProvider
}

type sourceCatalog struct {
	candidates []skill.Candidate
	warnings   []skill.Warning
	cleanup    func()
	localRoots map[string]string // stable SourceRef -> canonical checkout path
}

func addSourceCommandFlags(cmd *cobra.Command) {
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml")
	cmd.Flags().String("agent", "", "agent alvo no modo ad-hoc sem orotools.yaml")
	cmd.Flags().StringArray("local-root", nil, "checkout Git local autorizado (pode ser repetido)")
}

func newSkillsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "Lista skills por origem, identidade, versão, destino e conflito",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSkillsSourceList(cmd.Context(), sourceCommandOptionsFromFlags(cmd))
		},
	}
	addSourceCommandFlags(cmd)
	return cmd
}

func newSkillsInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "inspect",
		Short:        "Inspeciona uma skill pela identidade exata, sem fallback de origem",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := sourceCommandOptionsFromFlags(cmd)
			opts.id, _ = cmd.Flags().GetString("id")
			if opts.id == "" {
				return fmt.Errorf("informe --id com identidade exata exibida por `oro skills list`")
			}
			return runSkillsSourceInspect(cmd.Context(), opts)
		},
	}
	addSourceCommandFlags(cmd)
	cmd.Flags().String("id", "", "identidade exata da skill, não apenas nome")
	return cmd
}

func newSkillsInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "install",
		Short:        "Planeja e instala uma skill por identidade exata",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := sourceCommandOptionsFromFlags(cmd)
			opts.id, _ = cmd.Flags().GetString("id")
			opts.yes, _ = cmd.Flags().GetBool("yes")
			opts.dryRun, _ = cmd.Flags().GetBool("dry-run")
			if opts.id == "" {
				return fmt.Errorf("informe --id com identidade exata exibida por `oro skills list`")
			}
			return runSkillsSourceInstall(cmd.Context(), opts)
		},
	}
	addSourceCommandFlags(cmd)
	cmd.Flags().String("id", "", "identidade exata da skill, não apenas nome")
	cmd.Flags().Bool("yes", false, "confirma a aplicação do plano mostrado")
	cmd.Flags().Bool("dry-run", false, "mostra o plano sem instalar")
	return cmd
}

func sourceCommandOptionsFromFlags(cmd *cobra.Command) sourceCommandOptions {
	manifestPath, _ := cmd.Flags().GetString("manifest")
	agent, _ := cmd.Flags().GetString("agent")
	localRoots, _ := cmd.Flags().GetStringArray("local-root")
	return sourceCommandOptions{
		manifestPath: manifestPath, manifestExplicit: cmd.Flags().Changed("manifest"),
		agentFlag: agent, localRoots: localRoots, out: cmd.OutOrStdout(),
	}
}

func loadSourceCatalog(ctx context.Context, m *manifest.Manifest, opts sourceCommandOptions, includeRecipe, includePublic bool) (sourceCatalog, error) {
	result := sourceCatalog{cleanup: func() {}, localRoots: map[string]string{}}
	if ctx == nil {
		ctx = context.Background()
	}
	if includeRecipe && m != nil {
		candidates, err := loadBundledCatalog(skillsOptions{recipeCatalog: opts.recipeCatalog}, m)
		if err != nil {
			return result, fmt.Errorf("catálogo bundled: %w", err)
		}
		result.candidates = append(result.candidates, candidates...)
	}
	if includePublic {
		remote, warnings, cleanup, err := loadRemoteCatalogContext(ctx, skillsOptions{loadGitHub: opts.loadGitHub})
		if cleanup != nil {
			result.cleanup = cleanup
		}
		if err != nil {
			if len(opts.localRoots) == 0 && len(result.candidates) == 0 {
				return result, fmt.Errorf("catálogo público %s indisponível: %w", skill.GitHubCatalogRef, err)
			}
			result.warnings = append(result.warnings, skill.Warning{SkillID: skill.GitHubCatalogRef, Message: err.Error()})
		} else {
			result.candidates = append(result.candidates, remote...)
			result.warnings = append(result.warnings, warnings...)
		}
	}
	seenRoots := map[string]bool{}
	for _, rawRoot := range opts.localRoots {
		root, err := canonicalDirectory(rawRoot)
		if err != nil {
			result.cleanup()
			return result, fmt.Errorf("checkout local autorizado %q: %w", rawRoot, err)
		}
		if seenRoots[root] {
			result.cleanup()
			return result, fmt.Errorf("checkout local repetido: %s", root)
		}
		seenRoots[root] = true
		revision, err := inspectCleanGitCheckout(ctx, root)
		if err != nil {
			result.cleanup()
			return result, fmt.Errorf("checkout local %s não é uma origem limpa verificável: %w", root, err)
		}
		sourceRef := localCheckoutSourceRef(root)
		candidates, warnings, err := (skill.LocalCatalog{Root: root, SourceRef: sourceRef, Revision: revision}).List(ctx)
		if err != nil {
			result.cleanup()
			return result, fmt.Errorf("catálogo local %s: %w", sourceRef, err)
		}
		result.localRoots[sourceRef] = root
		result.candidates = append(result.candidates, candidates...)
		result.warnings = append(result.warnings, warnings...)
	}
	skill.SortCandidates(result.candidates)
	return result, nil
}

func localCheckoutSourceRef(root string) string {
	sum := sha256.Sum256([]byte(root))
	return "checkout-" + hex.EncodeToString(sum[:8])
}

func loadCandidateByExactID(ctx context.Context, opts sourceCommandOptions, m *manifest.Manifest) (skill.Candidate, sourceCatalog, error) {
	includeRecipe, includePublic, includeLocal := false, false, false
	switch {
	case strings.HasPrefix(opts.id, "recipe:"):
		includeRecipe = true
	case strings.HasPrefix(opts.id, skill.GitHubCatalogRef+"/"):
		includePublic = true
	case strings.HasPrefix(opts.id, "local:"):
		includeLocal = true
	default:
		return skill.Candidate{}, sourceCatalog{}, fmt.Errorf("identidade de skill desconhecida: %q", opts.id)
	}
	if includeLocal && len(opts.localRoots) == 0 {
		return skill.Candidate{}, sourceCatalog{}, fmt.Errorf("skill local exige checkout autorizado via --local-root; não haverá fallback para outra origem")
	}
	if !includeLocal {
		opts.localRoots = nil
	}
	if !includeRecipe {
		m = nil
	}
	catalog, err := loadSourceCatalog(ctx, m, opts, includeRecipe, includePublic)
	if err != nil {
		return skill.Candidate{}, catalog, err
	}
	matches := 0
	var found skill.Candidate
	for _, candidate := range catalog.candidates {
		if candidate.ID == opts.id {
			found, matches = candidate, matches+1
		}
	}
	if matches != 1 {
		catalog.cleanup()
		if matches > 1 {
			return skill.Candidate{}, catalog, fmt.Errorf("identidade duplicada no catálogo selecionado: %q", opts.id)
		}
		return skill.Candidate{}, catalog, fmt.Errorf("skill %q não encontrada na origem selecionada; fallback por nome/origem não é permitido", opts.id)
	}
	return found, catalog, nil
}

func sourceProjectContext(opts sourceCommandOptions) (skillsContext, error) {
	return loadSkillsContext(contextOptions{
		manifestPath: opts.manifestPath, manifestExplicit: opts.manifestExplicit,
		agentFlag: opts.agentFlag, interactive: false,
	})
}

func runSkillsSourceList(ctx context.Context, opts sourceCommandOptions) error {
	project, err := sourceProjectContext(opts)
	if err != nil {
		return fmt.Errorf("skills list: %w", err)
	}
	catalog, err := loadSourceCatalog(ctx, project.m, opts, project.m != nil, true)
	if err != nil {
		return fmt.Errorf("skills list: %w", err)
	}
	defer catalog.cleanup()
	for _, warning := range catalog.warnings {
		fmt.Fprintf(opts.out, "warning %s: %s\n", warning.SkillID, warning.Message)
	}
	collisions := make(map[string][]string)
	for _, candidate := range catalog.candidates {
		collisions[candidate.Name] = append(collisions[candidate.Name], candidate.ID)
	}
	fmt.Fprintln(opts.out, "Skills catalogadas (somente leitura)")
	for _, candidate := range catalog.candidates {
		plan, planErr := project.svc.PlanInstall(project.lock, candidate)
		target, targetErr := project.svc.TargetFor(candidate.Name)
		if targetErr != nil {
			target = "(inválido)"
		}
		decision, reason := "unavailable", ""
		if planErr != nil {
			reason = planErr.Error()
		} else {
			decision, reason = plan.Decision, plan.Reason
		}
		if len(collisions[candidate.Name]) > 1 {
			reason = appendReason(reason, "destination collision: "+strings.Join(collisions[candidate.Name], ", "))
		}
		origin := string(candidate.Source) + ":" + candidate.SourceRef
		if candidate.Source == skill.SourceLocal {
			origin = "local-checkout:" + catalog.localRoots[candidate.SourceRef]
		}
		fmt.Fprintf(opts.out, "- origin=%s id=%s name=%s version=%s target=%s decision=%s", origin, candidate.ID, candidate.Name, candidate.Version.String(), target, decision)
		if reason != "" {
			fmt.Fprintf(opts.out, " conflict=%s", reason)
		}
		if candidate.Description != "" {
			fmt.Fprintf(opts.out, " description=%s", candidate.Description)
		}
		fmt.Fprintln(opts.out)
	}
	if len(catalog.candidates) == 0 {
		fmt.Fprintln(opts.out, "Nenhuma skill encontrada nas origens selecionadas.")
	}
	return nil
}

func runSkillsSourceInspect(ctx context.Context, opts sourceCommandOptions) error {
	project, err := sourceProjectContext(opts)
	if err != nil {
		return fmt.Errorf("skills inspect: %w", err)
	}
	candidate, catalog, err := loadCandidateByExactID(ctx, opts, project.m)
	if err != nil {
		return fmt.Errorf("skills inspect: %w", err)
	}
	defer catalog.cleanup()
	plan, err := project.svc.PlanInstall(project.lock, candidate)
	if err != nil {
		return fmt.Errorf("skills inspect: plan: %w", err)
	}
	printSkillSourcePlan(opts.out, candidate, plan, catalog.localRoots)
	return nil
}

func runSkillsSourceInstall(ctx context.Context, opts sourceCommandOptions) error {
	if opts.yes && opts.dryRun {
		return fmt.Errorf("skills install: --yes e --dry-run não podem ser combinados")
	}
	project, err := sourceProjectContext(opts)
	if err != nil {
		return fmt.Errorf("skills install: %w", err)
	}
	candidate, catalog, err := loadCandidateByExactID(ctx, opts, project.m)
	if err != nil {
		return fmt.Errorf("skills install: %w", err)
	}
	defer catalog.cleanup()
	plan, err := project.svc.PlanInstall(project.lock, candidate)
	if err != nil {
		return fmt.Errorf("skills install: plan: %w", err)
	}
	printSkillSourcePlan(opts.out, candidate, plan, catalog.localRoots)
	if plan.Decision == "blocked" {
		return fmt.Errorf("skills install: conflito bloqueia aplicação: %s", plan.Reason)
	}
	if opts.dryRun || plan.Decision == "unchanged" {
		return nil
	}
	if !opts.yes {
		return fmt.Errorf("skills install: preview only; passe --yes para aplicar exatamente este plano")
	}
	result, err := applySkillInstallPlan(ctx, project.svc, project.m, project.lock, candidate, plan, catalog.localRoots[candidate.SourceRef], plan.StateFingerprint)
	if err != nil {
		return fmt.Errorf("skills install: %w", err)
	}
	fmt.Fprintf(opts.out, "Instalada %s %s em %s\n", result.Name, result.Version, result.Target)
	return nil
}

func applySkillInstallPlan(ctx context.Context, svc skill.Service, m *manifest.Manifest, lock *skill.Lock, candidate skill.Candidate, approved skill.InstallPlan, localRoot, expectedStateFingerprint string) (skill.InstallResult, error) {
	if candidate.Source == skill.SourceLocal {
		revision, err := inspectCleanGitCheckout(ctx, localRoot)
		if err != nil || revision != candidate.Revision {
			return skill.InstallResult{}, fmt.Errorf("local checkout is dirty or revision changed since approval")
		}
	}
	plan, err := svc.PlanInstall(lock, candidate)
	if err != nil {
		return skill.InstallResult{}, fmt.Errorf("revalidate approved install plan: %w", err)
	}
	if expectedStateFingerprint == "" || plan.StateFingerprint != expectedStateFingerprint {
		return skill.InstallResult{}, fmt.Errorf("project state changed since approval; create a new preview and explicitly confirm it")
	}
	if !sameApprovedInstallPlan(plan, approved) {
		return skill.InstallResult{}, fmt.Errorf("approved install plan changed; require a new preview and explicit confirmation")
	}
	if plan.Decision == "blocked" {
		return skill.InstallResult{}, fmt.Errorf("install blocked: %s", plan.Reason)
	}
	if plan.Decision != "install" && plan.Decision != "unchanged" {
		return skill.InstallResult{}, fmt.Errorf("unexpected plan decision %q", plan.Decision)
	}
	result, _, err := svc.ApplyPlannedInstallContext(ctx, m, lock, candidate, plan.PlanToken)
	return result, err
}

func sameApprovedInstallPlan(a, b skill.InstallPlan) bool {
	return a.SkillID == b.SkillID && a.Name == b.Name && a.Source == b.Source && a.SourceRef == b.SourceRef &&
		a.SourcePath == b.SourcePath && a.SourceRevision == b.SourceRevision && a.Version == b.Version &&
		a.Agent == b.Agent && a.Target == b.Target && a.ContentDigest == b.ContentDigest && a.Decision == b.Decision
}

func printSkillSourcePlan(out io.Writer, candidate skill.Candidate, plan skill.InstallPlan, localRoots map[string]string) {
	origin := string(candidate.Source) + ":" + candidate.SourceRef
	if candidate.Source == skill.SourceLocal {
		origin = "local-checkout:" + localRoots[candidate.SourceRef]
	}
	fmt.Fprintf(out, "origin=%s\nid=%s\nname=%s\nversion=%s\nsourcePath=%s\nsourceRevision=%s\ntarget=%s\ndecision=%s\n", origin, candidate.ID, candidate.Name, candidate.Version.String(), candidate.Path, candidate.Revision, plan.Target, plan.Decision)
	if plan.Reason != "" {
		fmt.Fprintf(out, "conflict=%s\n", plan.Reason)
	}
}

func appendReason(current, addition string) string {
	if current == "" {
		return addition
	}
	return current + "; " + addition
}
