package cli

import (
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [skill|agent|pipeline] [value]",
		Short: "Registra uma skill, agent ou provider de pipeline no orotools.yaml",
		Long: `Edita o orotools.yaml para registrar uma nova entrada:

  oro add skill code-review
  oro add agent reviewer
  oro add pipeline azure-pipelines

Na v0.1 isto atualiza apenas o manifest; a instalação/geração de fato
acontece nas fases dos providers (13-15).`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("manifest")
			return runAdd(addOptions{
				kind:         args[0],
				value:        args[1],
				manifestPath: path,
				out:          cmd.OutOrStdout(),
			})
		},
	}
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml")
	return cmd
}
