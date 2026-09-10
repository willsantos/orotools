package cli

import (
	"github.com/spf13/cobra"
)

// activeVersion guarda a versão do binário para as UIs interativas (o picker
// de skills mostra no cabeçalho). Fica vazia em testes que chamam runSkills
// direto — o picker degrada para "dev".
var activeVersion string

// Run builds the root command and executes it with args.
func Run(version string, args []string) error {
	if shouldCheckForUpdate(version, args) {
		// Aviso passivo de release nova (DIST-13): fire-and-forget, não
		// bloqueia nem atrasa o comando (NFR-1).
		go runPassiveUpdateCheck(version)
	}
	root := newRoot(version)
	root.SetArgs(args)
	return root.Execute()
}

func newRoot(version string) *cobra.Command {
	activeVersion = version
	root := &cobra.Command{
		Use:   "oro",
		Short: "Orotools — scaffold e padronização de projetos",
	}
	root.Version = version
	root.SetVersionTemplate("oro {{.Version}}\n")
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInfoCmd())
	root.AddCommand(newNewCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newAddCmd())
	root.AddCommand(newRecipeCmd())
	root.AddCommand(newDevCmd())
	root.AddCommand(newSkillsCmd())
	root.AddCommand(newUpgradeCmd(version))
	return root
}
