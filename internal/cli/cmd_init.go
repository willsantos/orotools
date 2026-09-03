package cli

import (
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Detecta a stack do projeto atual e gera o orotools.yaml",
		Long: `Inspeciona o diretório atual por marcadores conhecidos (Gemfile, *.csproj,
package.json, lockfiles, .git) e escreve um orotools.yaml inicial com a
recipe sugerida (seção 55 da spec).`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			return runInit(initOptions{
				yes: yes,
				out: cmd.OutOrStdout(),
			})
		},
	}
	cmd.Flags().Bool("yes", false, "escreve o orotools.yaml sem perguntar")
	return cmd
}
