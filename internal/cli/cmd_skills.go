package cli

import (
	"github.com/spf13/cobra"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Instala skills da recipe e do catálogo público skills_AI",
		Long: `Assistente interativo de instalação de skills gerenciadas:

  - catálogo bundled da recipe do manifest (embutido no binário)
  - catálogo público github:willsantos/skills_AI

Skills já instaladas são exibidas e não são removidas nem
reinstaladas. O estado instalado fica em .orotools/skills.lock.yaml.
Para atualizar tudo que está gerenciado, use "oro skills update".

Fora de um projeto Oro (sem orotools.yaml) o wizard funciona em
modo ad-hoc: instala apenas do catálogo skills_AI e detecta o
destino pelas pastas de skills existentes (ou --agent).`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("manifest")
			agent, _ := cmd.Flags().GetString("agent")
			return runSkills(skillsOptions{
				manifestPath:     path,
				manifestExplicit: cmd.Flags().Changed("manifest"),
				agentFlag:        agent,
				out:              cmd.OutOrStdout(),
				errOut:           cmd.ErrOrStderr(),
				stdin:            cmd.InOrStdin(),
			})
		},
	}
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml")
	cmd.Flags().String("agent", "", "agent alvo no modo ad-hoc (sem orotools.yaml): opencode|cursor|claude-code|codex|copilot")
	cmd.AddCommand(newSkillsUpdateCmd())
	return cmd
}

func newSkillsUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Atualiza todas as skills gerenciadas do projeto",
		Long: `Compara cada skill registrada em .orotools/skills.lock.yaml com o
catálogo (recipe embutida + github:willsantos/skills_AI) e aplica
as atualizações — atomicamente por skill, best-effort no lote.

Skills com alterações locais são bloqueadas; use --force para
substituí-las (as mudanças locais serão perdidas). Use --dry-run
para ver as decisões sem modificar nada.

Sem orotools.yaml, atualiza apenas as skills do catálogo GitHub
registradas no lock; o agent é deduzido dos targets do lock ou
informado via --agent.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("manifest")
			agent, _ := cmd.Flags().GetString("agent")
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return runSkillsUpdate(skillsUpdateOptions{
				manifestPath:     path,
				manifestExplicit: cmd.Flags().Changed("manifest"),
				agentFlag:        agent,
				force:            force,
				dryRun:           dryRun,
				out:              cmd.OutOrStdout(),
				errOut:           cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml")
	cmd.Flags().String("agent", "", "agent alvo no modo ad-hoc (sem orotools.yaml): opencode|cursor|claude-code|codex|copilot")
	cmd.Flags().Bool("force", false, "substitui skills com alterações locais (as mudanças serão perdidas)")
	cmd.Flags().Bool("dry-run", false, "mostra as decisões sem modificar nada")
	return cmd
}
