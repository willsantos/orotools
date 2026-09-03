package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/ui"
	"oroborus.dev/orotools/internal/updater"
)

const (
	// NFR-1: --check responde rápido mesmo com o canal lento; o apply tem
	// folga para download em rede lenta.
	upgradeCheckTimeout = 10 * time.Second
	upgradeApplyTimeout = 5 * time.Minute
)

type upgradeOptions struct {
	// Current é a versão do binário (main.version): "dev" ou semver.
	Current string
	// CheckOnly reporta e sai (exit 0), sem tocar no binário (--check).
	CheckOnly bool
	// Target atualiza para uma versão específica (--version vX.Y.Z); vazio
	// significa a última release.
	Target string
	// Executable é o caminho do binário a substituir; vazio usa
	// os.Executable(). Injetável para testes.
	Executable string
	// BaseURL sobrepõe o canal de releases (testes / canal alternativo).
	BaseURL string
}

func newUpgradeCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Atualiza o oro para a última release do canal público",
		Long: `Baixa a release mais recente de ` + updater.Owner + `/` + updater.Repo + `,
valida o checksum sha256 e substitui o binário atual com rename atômico.
Com --check apenas verifica se há versão nova. Com --version atualiza
para uma release específica.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checkOnly, _ := cmd.Flags().GetBool("check")
			target, _ := cmd.Flags().GetString("version")
			return runUpgrade(cmd.Context(), cmd.OutOrStdout(), upgradeOptions{
				Current:   version,
				CheckOnly: checkOnly,
				Target:    target,
			})
		},
	}
	cmd.Flags().Bool("check", false, "apenas verifica se há versão nova, sem atualizar")
	cmd.Flags().String("version", "", "atualiza para uma versão específica (ex.: v0.5.0)")
	return cmd
}

func runUpgrade(ctx context.Context, w io.Writer, opts upgradeOptions) error {
	s := ui.NewStatus(w)

	up, err := updater.New(updater.Options{BaseURL: opts.BaseURL})
	if err != nil {
		s.Error(err.Error())
		return err
	}

	// Resolução da release alvo sob timeout curto: falha de rede não pode
	// travar o comando (NFR-1); a mensagem sempre oferece o caminho manual.
	resolveCtx, cancel := context.WithTimeout(ctx, upgradeCheckTimeout)
	defer cancel()

	rel, err := resolveRelease(resolveCtx, up, opts.Target)
	if err != nil {
		s.Error(err.Error())
		return fmt.Errorf("não foi possível consultar o canal de releases — alternativa manual: %s", updater.ReleasesURL)
	}
	target := displayVersion(rel.Version)

	newer, err := updater.IsNewer(rel.Version, opts.Current)
	switch {
	case errors.Is(err, updater.ErrUnknownCurrentVersion):
		s.Warn(fmt.Sprintf("versão local é um build sem release (%q): não há como comparar — instale uma release com %s",
			opts.Current, "oro upgrade --version "+target))
	case err != nil:
		s.Error(err.Error())
		return err
	}

	if opts.CheckOnly {
		switch {
		case newer:
			s.Info(fmt.Sprintf("nova versão disponível: %s → %s (rode \"oro upgrade\")", displayVersion(opts.Current), target))
		case errors.Is(err, updater.ErrUnknownCurrentVersion):
			s.Info("última release do canal: " + target)
		default:
			s.Success(fmt.Sprintf("oro está na última versão (%s)", displayVersion(opts.Current)))
		}
		s.Info("release: " + rel.URL)
		return nil
	}

	if opts.Target == "" && !newer && !errors.Is(err, updater.ErrUnknownCurrentVersion) {
		s.Success(fmt.Sprintf("oro está na última versão (%s) — nada a fazer", displayVersion(opts.Current)))
		return nil
	}

	exe := opts.Executable
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			s.Error("não foi possível localizar o binário do oro: " + err.Error())
			return err
		}
	}

	s.Info(fmt.Sprintf("baixando %s e validando checksum...", target))
	applyCtx, cancelApply := context.WithTimeout(ctx, upgradeApplyTimeout)
	defer cancelApply()
	if err := up.Apply(applyCtx, rel, exe); err != nil {
		if errors.Is(err, updater.ErrNotWritable) {
			dir := exe
			s.Error(fmt.Sprintf("sem permissão para substituir o binário (%s): atualize pelo gerenciador de pacotes que o instalou (dpkg/rpm)", dir))
			return err
		}
		s.Error(err.Error())
		return err
	}
	s.Success(fmt.Sprintf("oro atualizado para %s — a nova versão vale para as próximas execuções", target))
	return nil
}

func resolveRelease(ctx context.Context, up *updater.Updater, target string) (*updater.Release, error) {
	if target != "" {
		return up.Version(ctx, target)
	}
	return up.Latest(ctx)
}

func displayVersion(v string) string {
	if v == "" || v == "dev" {
		return v
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
