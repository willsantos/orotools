package cli

import (
	"github.com/spf13/cobra"
)

func newRecipeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipe",
		Short: "Inspeciona recipes oficiais",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista as recipes oficiais embutidas",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecipeList(cmd.OutOrStdout())
		},
	})
	return cmd
}
