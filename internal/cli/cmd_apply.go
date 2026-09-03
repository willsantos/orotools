package cli

import (
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcilia o projeto atual com o orotools.yaml e sua recipe",
		Long: `Re-executa a recipe contra o projeto atual. Passos já satisfeitos são
pulados (idempotente), então o apply recupera projetos parcialmente
configurados (seção 56 da spec). Execute na raiz do projeto.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			recipePath, _ := cmd.Flags().GetString("recipe")
			stack, _ := cmd.Flags().GetString("stack")
			manifestPath, _ := cmd.Flags().GetString("manifest")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return runApply(applyOptions{
				stack:      stack,
				recipePath: recipePath,
				manifest:   manifestPath,
				dryRun:     dryRun,
				out:        cmd.OutOrStdout(),
				err:        cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().String("recipe", "", "caminho para um recipe.yaml customizado")
	cmd.Flags().String("stack", "", "recipe oficial (padrão: recipe.name do manifest)")
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml")
	cmd.Flags().Bool("dry-run", false, "exibe o plano sem executar")
	return cmd
}
