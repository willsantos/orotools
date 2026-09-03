package cli

import (
	"github.com/spf13/cobra"
)

func newNewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new [nome]",
		Short: "Cria um novo projeto a partir de uma recipe",
		Long: `Cria um novo projeto scaffolded a partir de uma recipe.

  oro new meu-app --stack fastify-next --yes --set database=postgres

Variáveis não fornecidas via --set e sem default são perguntadas
interativamente, a menos que --yes seja usado.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			recipePath, _ := cmd.Flags().GetString("recipe")
			stack, _ := cmd.Flags().GetString("stack")
			set, _ := cmd.Flags().GetStringArray("set")
			yes, _ := cmd.Flags().GetBool("yes")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			repositoryProvider, _ := cmd.Flags().GetString("repository")
			pipelineProvider, _ := cmd.Flags().GetString("pipeline")
			aiAgent, _ := cmd.Flags().GetString("ai")
			remote, _ := cmd.Flags().GetString("remote")
			visibility, _ := cmd.Flags().GetString("visibility")
			organization, _ := cmd.Flags().GetString("organization")
			azureProject, _ := cmd.Flags().GetString("azure-project")
			return runNew(newOptions{
				name:         args[0],
				stack:        stack,
				recipePath:   recipePath,
				set:          set,
				yes:          yes,
				dryRun:       dryRun,
				repository:   repositoryProvider,
				pipeline:     pipelineProvider,
				ai:           aiAgent,
				remote:       remote,
				visibility:   visibility,
				organization: organization,
				azureProject: azureProject,
				out:          cmd.OutOrStdout(),
				err:          cmd.ErrOrStderr(),
				stdin:        cmd.InOrStdin(),
			})
		},
	}
	cmd.Flags().String("recipe", "", "caminho para um recipe.yaml customizado")
	cmd.Flags().String("stack", "", "recipe oficial: dotnet|dotnet-next|rails|fastify-next")
	cmd.Flags().StringArray("set", nil, "define uma variável (chave=valor, repetível)")
	cmd.Flags().Bool("yes", false, "não-interativo: usa apenas --set e defaults")
	cmd.Flags().Bool("dry-run", false, "exibe o plano sem executar")
	cmd.Flags().String("repository", "local", "provider de repositório: local|github|azure-devops")
	cmd.Flags().String("pipeline", "", "provider de pipeline: none|github-actions|azure-pipelines (padrão derivado do repositório)")
	cmd.Flags().String("ai", "opencode", "agent de IA alvo: opencode|cursor|claude-code|codex|copilot")
	cmd.Flags().String("remote", "", "URL explícita do remote Git")
	cmd.Flags().String("visibility", "private", "visibilidade no GitHub: private|public")
	cmd.Flags().String("organization", "", "URL da organização do Azure DevOps")
	cmd.Flags().String("azure-project", "", "nome do projeto no Azure DevOps")
	return cmd
}
