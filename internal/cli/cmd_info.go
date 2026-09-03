package cli

import (
	"github.com/spf13/cobra"
)

func newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "info",
		Short:        "Exibe o estado do projeto a partir do orotools.yaml",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("manifest")
			return runInfo(cmd.OutOrStdout(), path)
		},
	}
	cmd.Flags().String("manifest", manifestFilename, "caminho para o arquivo de manifest")
	return cmd
}
