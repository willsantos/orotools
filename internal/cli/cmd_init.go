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
recipe sugerida (seção 55 da spec).

Com --onboarding, após escrever o manifest o comando planeja a adoção Git
gerenciada (bloco de .gitignore e, com --protect-branches, hooks locais),
mostra o preview somente leitura e aplica apenas com --yes. Conflitos
(core.hooksPath, hooks existentes, linked worktrees) bloqueiam só a adoção e
nunca sobrescrevem configuração existente; o init permanece bem-sucedido.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			onboarding, _ := cmd.Flags().GetBool("onboarding")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			branches := flagString(cmd, "protect-branches", "")
			return runInit(initOptions{
				yes:             yes,
				onboarding:      onboarding,
				dryRun:          dryRun,
				branches:        branches,
				branchesChanged: cmd.Flags().Changed("protect-branches"),
				out:             cmd.OutOrStdout(),
			})
		},
	}
	cmd.Flags().Bool("yes", false, "escreve o orotools.yaml sem perguntar")
	cmd.Flags().Bool("onboarding", false, "após o manifest, planeja e (com --yes) aplica onboarding Git opt-in")
	cmd.Flags().String("protect-branches", "", "com --onboarding: habilita hooks de proteção para branches separadas por vírgula")
	cmd.Flags().Bool("dry-run", false, "apenas preview; nenhum arquivo é escrito")
	return cmd
}
