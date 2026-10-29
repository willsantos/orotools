package cli

import "github.com/spf13/cobra"

func newOnboardingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "onboarding",
		Short: "Inspeciona e aplica onboarding Git local de forma opt-in",
		Long: `Mostra preview somente leitura por padrão. Instalação e remoção exigem
subcomando explícito e --yes. Hooks de proteção exigem --protect-branches
explícito em plan/apply; nenhum hook é instalado automaticamente por init.`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().String("path", ".", "raiz do projeto Git alvo")
	cmd.AddCommand(newOnboardingPlanCmd())
	cmd.AddCommand(newOnboardingApplyCmd())
	cmd.AddCommand(newOnboardingUninstallCmd())
	return cmd
}

func newOnboardingPlanCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Aliases: []string{"preview"}, Short: "Exibe plano somente leitura", Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		return runOnboardingPlan(onboardingCLIOptions{path: flagString(cmd, "path", "."), branches: flagString(cmd, "protect-branches", ""), branchesChanged: cmd.Flags().Changed("protect-branches"), out: cmd.OutOrStdout()})
	}}
	cmd.Flags().String("protect-branches", "", "habilita hooks de proteção para branches explícitas separadas por vírgula")
	return cmd
}

func newOnboardingApplyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "apply", Short: "Aplica onboarding após confirmação explícita", Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		return runOnboardingApply(onboardingCLIOptions{path: flagString(cmd, "path", "."), branches: flagString(cmd, "protect-branches", ""), branchesChanged: cmd.Flags().Changed("protect-branches"), yes: flagBool(cmd, "yes"), dryRun: flagBool(cmd, "dry-run"), out: cmd.OutOrStdout()})
	}}
	cmd.Flags().String("protect-branches", "", "habilita hooks de proteção para branches explícitas separadas por vírgula")
	cmd.Flags().Bool("yes", false, "confirma aplicação explícita após exibir preview")
	cmd.Flags().Bool("dry-run", false, "exibe preview sem aplicar")
	return cmd
}

func newOnboardingUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "uninstall", Short: "Remove onboarding gerenciado íntegro após confirmação", Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		return runOnboardingUninstall(onboardingCLIOptions{path: flagString(cmd, "path", "."), yes: flagBool(cmd, "yes"), out: cmd.OutOrStdout()})
	}}
	cmd.Flags().Bool("yes", false, "confirma remoção explícita após exibir preview")
	return cmd
}

func flagString(cmd *cobra.Command, name, fallback string) string {
	if v, e := cmd.Flags().GetString(name); e == nil {
		return v
	}
	v, e := cmd.InheritedFlags().GetString(name)
	if e == nil {
		return v
	}
	return fallback
}
func flagBool(cmd *cobra.Command, name string) bool { v, _ := cmd.Flags().GetBool(name); return v }
