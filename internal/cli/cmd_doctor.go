package cli

import (
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Valida os requisitos de ambiente declarados por uma recipe",
		Long: `Confere se toda ferramenta exigida pela recipe está no PATH e satisfaz a
restrição de versão declarada.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			recipePath, _ := cmd.Flags().GetString("recipe")
			stack, _ := cmd.Flags().GetString("stack")
			return runDoctor(cmd.OutOrStdout(), stack, recipePath)
		},
	}
	cmd.Flags().String("recipe", "", "caminho para um recipe.yaml customizado")
	cmd.Flags().String("stack", "", "recipe oficial: dotnet|dotnet-next|rails|fastify-next")
	return cmd
}
