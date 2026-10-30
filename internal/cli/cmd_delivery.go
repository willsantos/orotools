package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/delivery"
)

// deliveryTimeout limita a verificação do checkout de standards (Git local,
// sem rede). É o mesmo orçamento do protocolo de skills.
const deliveryTimeout = 45 * time.Second

func newDeliveryCmd() *cobra.Command {
	deliveryCmd := &cobra.Command{
		Use:   "delivery",
		Short: "Geração oficial de delivery a partir dos standards pinados (preview/write)",
		Long: "Gera .oroborus/delivery.yaml e azure-pipelines.yml consumindo o template oficial\n" +
			"registrado em um checkout verificado dos engineering standards (ADR-0004).\n" +
			"Por padrão apenas exibe o plano (preview); use --write para aplicar. Nunca faz deploy,\n" +
			"nunca executa build.command e nunca sobrescreve CI existente.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	deliveryCmd.AddCommand(newDeliveryGenerateCmd())
	return deliveryCmd
}

func newDeliveryGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Planeja (default) ou escreve os arquivos de delivery oficiais",
		Long: "Verifica um checkout limpo dos standards (--standards-root), detecta a stack do\n" +
			"projeto e valida o .oroborus/delivery.yaml renderizado contra o schema oficial\n" +
			"ANTES de qualquer escrita. Sem --write, nada é escrito.\n\n" +
			"Dados que a evidência não consegue fornecer (deploy-provider, promotion-mode,\n" +
			"ambientes) precisam ser passados explicitamente: a geração não inventa dados.",
		Args: cobra.NoArgs,
		RunE: runDeliveryGenerate,
	}
	cmd.Flags().String("application", "", "identificador da aplicação no delivery (obrigatório; minúsculas, dígitos e traços)")
	cmd.Flags().String("provider", "azure-devops", "provedor de CI do pipeline gerado (apenas azure-devops implementado)")
	cmd.Flags().String("standards-root", "", "raiz de um checkout Git LIMPO dos engineering standards (obrigatório)")
	cmd.Flags().String("project", ".", "raiz do projeto alvo")
	cmd.Flags().Bool("write", false, "escreve os arquivos (padrão: apenas preview; recusa sobrescrever existentes)")
	cmd.Flags().String("component", "app", "nome do componente único gerado")
	cmd.Flags().String("deploy-provider", "", "provedor de deployment do componente (docker|cloudflare-pages|cloudflare-workers|vercel|static|other)")
	cmd.Flags().String("promotion-mode", "", "modo de promoção (same-artifact|native-version-activation|reupload-same-files|platform-rebuild)")
	cmd.Flags().String("env-dev", "", "target do ambiente dev (obrigatório)")
	cmd.Flags().String("env-prd", "", "target do ambiente prd (obrigatório)")
	cmd.Flags().String("env-stg", "", "target do ambiente stg (opcional)")
	cmd.Flags().String("build-command", "", "comando de build informativo explícito (sobrepõe o derivado da stack)")
	cmd.Flags().String("standards-repo", "Engenharia/oroborus-engineering-standards", "repositório Azure DevOps do resource 'standards' no pipeline gerado")
	return cmd
}

func runDeliveryGenerate(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()
	application, _ := flags.GetString("application")
	provider, _ := flags.GetString("provider")
	standardsRoot, _ := flags.GetString("standards-root")
	project, _ := flags.GetString("project")
	write, _ := flags.GetBool("write")
	component, _ := flags.GetString("component")
	deployProvider, _ := flags.GetString("deploy-provider")
	promotionMode, _ := flags.GetString("promotion-mode")
	envDev, _ := flags.GetString("env-dev")
	envPrd, _ := flags.GetString("env-prd")
	envStg, _ := flags.GetString("env-stg")
	buildCommand, _ := flags.GetString("build-command")
	standardsRepo, _ := flags.GetString("standards-repo")

	out := cmd.OutOrStdout()
	// Sem verificação de checkout de standards a geração falha fechada, com a
	// mesma postura anterior ao M05.4.
	if strings.TrimSpace(standardsRoot) == "" {
		return errors.New("delivery generation unavailable: a verified official standards source is required — pass --standards-root pointing to the root of a clean Git checkout of the engineering standards distribution")
	}
	project, err := cleanPathArg(project)
	if err != nil {
		return err
	}
	standardsRoot, err = canonicalDirectory(standardsRoot)
	if err != nil {
		return fmt.Errorf("--standards-root: %w", err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), deliveryTimeout)
	defer cancel()
	// A camada de CLI é responsável pelo Git: delivery recebe apenas o
	// snapshot verificado (revisão HEAD de um checkout limpo).
	revision, err := verifyStandardsCheckout(ctx, standardsRoot)
	if err != nil {
		return fmt.Errorf("standards checkout verification failed: %w", err)
	}
	source, err := delivery.LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		return err
	}
	plan, err := delivery.PlanGeneration(project, source, delivery.Options{
		Application:       application,
		Component:         component,
		Provider:          provider,
		DeployProvider:    deployProvider,
		PromotionMode:     promotionMode,
		EnvDev:            envDev,
		EnvPrd:            envPrd,
		EnvStg:            envStg,
		BuildCommand:      buildCommand,
		StandardsRepoName: standardsRepo,
	})
	if err != nil {
		return err
	}
	printDeliveryPlan(out, plan)
	if !write {
		fmt.Fprintln(out, "Preview: nada foi escrito. Rode com --write para criar exatamente os arquivos listados.")
		return nil
	}
	if err := plan.Write(project); err != nil {
		return err
	}
	fmt.Fprintln(out, "Escrito: .oroborus/delivery.yaml, azure-pipelines.yml (criação exclusiva; nada existente foi sobrescrito).")
	return nil
}

func printDeliveryPlan(out interface{ Write([]byte) (int, error) }, plan *delivery.Plan) {
	fmt.Fprintln(out, "oro delivery generate — plano")
	fmt.Fprintf(out, "  projeto: %s\n", plan.ProjectRoot)
	if plan.Stack != "" {
		fmt.Fprintf(out, "  stack detectada: %s\n", plan.Stack)
		if plan.BuildCommand != "" {
			fmt.Fprintf(out, "  build.command: %s (origem: %s; informativo — a geração NÃO o executa)\n", plan.BuildCommand, plan.BuildCommandSource)
		}
	} else {
		fmt.Fprintf(out, "  stack: não classificada (%s)\n", plan.StackNote)
		fmt.Fprintf(out, "  build.command: omitido — sem evidência, nenhum comando fictício é gerado (use --build-command para informar)\n")
	}
	fmt.Fprintln(out, "  standards verificados:")
	fmt.Fprintf(out, "    revision: %s (HEAD de checkout limpo)\n", plan.Pin.StandardsRevision)
	fmt.Fprintf(out, "    distributionVersion: %s\n", plan.Pin.DistributionVersion)
	fmt.Fprintf(out, "    template: %s (%s)\n", plan.Pin.TemplateID, plan.Pin.TemplatePath)
	fmt.Fprintln(out, "  validação do schema oficial: OK")
	fmt.Fprintln(out, "  arquivos que seriam criados:")
	for _, file := range plan.Files {
		fmt.Fprintf(out, "    %s\n", file)
	}
	if len(plan.Conflicts) == 0 {
		fmt.Fprintln(out, "  conflitos: nenhum")
	} else {
		fmt.Fprintln(out, "  conflitos (nada seria sobrescrito; adoção de CI existente é manual):")
		for _, conflict := range plan.Conflicts {
			fmt.Fprintf(out, "    %s já existe\n", conflict)
		}
	}
}

// verifyStandardsCheckout verifies that root is the exact toplevel of a Git
// checkout whose TRACKED working tree is byte/mode-equal to HEAD, and returns
// the full HEAD SHA. It is the same verification approach as the skills
// protocol's inspectCleanGitCheckout (controlled git env, exact toplevel,
// per-blob byte/mode equality, symlinks refused) with one deliberate
// divergence: the equality assertion covers the tracked set, not every
// physical file. Untracked/gitignored artifacts of a developer checkout (e.g.
// a local .venv) never enter the verified set — generation only ever reads
// files that are verified against the pinned HEAD blobs, through symlink-
// refusing reads — so they cannot influence the snapshot and are not walked.
func verifyStandardsCheckout(ctx context.Context, root string) (string, error) {
	run := func(args ...string) ([]byte, error) {
		gitArgs := append([]string{"--no-lazy-fetch", "-c", "protocol.allow=never", "-c", "protocol.ext.allow=never", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.attributesFile=/dev/null", "-C", root}, args...)
		cmd := exec.CommandContext(ctx, "git", gitArgs...)
		cmd.Env = controlledGitEnv(os.Environ())
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return out, nil
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(head))
	if !fullCommitSHA.MatchString(revision) {
		return "", errors.New("standards checkout HEAD is not a full commit SHA")
	}
	rootFromGit, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	gitRoot, err := canonicalDirectory(strings.TrimSpace(string(rootFromGit)))
	if err != nil || gitRoot != root {
		return "", errors.New("--standards-root must be the exact root of a Git checkout")
	}
	listing, err := run("ls-tree", "-r", "-z", "--full-tree", revision)
	if err != nil {
		return "", err
	}
	verified := 0
	for _, record := range strings.Split(string(listing), "\x00") {
		if record == "" {
			continue
		}
		metadata, name, ok := strings.Cut(record, "\t")
		if !ok {
			return "", errors.New("malformed HEAD tree entry")
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 || fields[1] != "blob" {
			return "", fmt.Errorf("unsupported HEAD object entry %q (symlinks and submodules are refused)", name)
		}
		if fields[0] != "100644" && fields[0] != "100755" {
			return "", fmt.Errorf("unsupported HEAD file mode %q for %q", fields[0], name)
		}
		physical, err := delivery.LstatFileInsideRoot(root, name)
		if err != nil {
			return "", fmt.Errorf("tracked file %q: %w", name, err)
		}
		if physical.Mode()&os.ModeSymlink != 0 || !physical.Mode().IsRegular() {
			return "", fmt.Errorf("tracked file %q must be a regular file (symlinks are refused)", name)
		}
		if executable := physical.Mode().Perm()&0o111 != 0; executable != (fields[0] == "100755") {
			return "", fmt.Errorf("file mode differs from HEAD for %q", name)
		}
		blob, err := run("cat-file", "blob", fields[2])
		if err != nil {
			return "", err
		}
		working, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return "", fmt.Errorf("read tracked file %q: %w", name, err)
		}
		if !bytes.Equal(blob, working) {
			return "", fmt.Errorf("tracked file %q differs from pinned HEAD; use a clean checkout (commit or revert local changes)", name)
		}
		verified++
	}
	if verified == 0 {
		return "", errors.New("standards checkout has an empty HEAD tree")
	}
	return revision, nil
}
