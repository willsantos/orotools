package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/skill"
	"oroborus.dev/orotools/internal/ui"
)

// skillsRecoverOptions carries `oro skills recover` inputs.
type skillsRecoverOptions struct {
	manifestPath   string
	rollback       bool
	releaseStale   bool
	force          bool
	out            io.Writer
	errOut         io.Writer
	inspectLock    func(string) (skill.OperationLockInfo, error)
	inspectJournal func(string) (skill.TransactionInfo, error)
	releaseStaleFn func(string, bool) (skill.OperationLockInfo, error)
	rollbackFn     func(string) (skill.RollbackResult, error)
}

func newSkillsRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Inspeciona (e recupera explicitamente) locks de processo e journals",
		Long: `Inspeção somente leitura por padrão: mostra o estado do lock de
processo (.orotools/.skills-operation.lock, com verificação de vida do
pid dono) e do journal de transação (.orotools/.skills-transaction.json:
alvo, stage, backup, presença de manifest/lock anteriores).

O motor NUNCA recupera automaticamente (fail-closed). Esta é a via
explícita de recuperação:

  --rollback           restaura o lock e/ou o manifest anteriores a partir
                       dos payloads do journal, devolve o diretório alvo ao
                       estado anterior (backup de volta, ou alvo de instalação
                       removido), remove o stage somente se marcado como
                       nosso, e por fim remove o journal. Sem journal é no-op.
                       Payload ausente/corrompido ou backup desaparecido:
                       falha SEM alterar nada (fail-closed, nunca inventa
                       estado). Idempotente.

  --release-stale-lock remove o lock de processo somente quando o pid do
                       dono está veriavelmente morto; dono vivo é recusado;
                       em plataformas sem verificação de pid é exigido
                       --force (e a verificação manual do operador).

Use uma ação por execução. O lock de processo é adquirido antes do
rollback; um dono VIVO bloqueia a recuperação.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manifestPath, _ := cmd.Flags().GetString("manifest")
			rollback, _ := cmd.Flags().GetBool("rollback")
			releaseStale, _ := cmd.Flags().GetBool("release-stale-lock")
			force, _ := cmd.Flags().GetBool("force")
			return runSkillsRecover(skillsRecoverOptions{
				manifestPath: manifestPath,
				rollback:     rollback,
				releaseStale: releaseStale,
				force:        force,
				out:          cmd.OutOrStdout(),
				errOut:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml (a raiz do projeto é o diretório dele)")
	cmd.Flags().Bool("rollback", false, "recuperação explícita a partir do journal de transação")
	cmd.Flags().Bool("release-stale-lock", false, "libera o lock de processo com dono morto verificado")
	cmd.Flags().Bool("force", false, "exigido por --release-stale-lock quando a plataforma não verifica pids")
	return cmd
}

func runSkillsRecover(opts skillsRecoverOptions) error {
	if opts.rollback && opts.releaseStale {
		return errors.New("skills recover: use --rollback OU --release-stale-lock, uma ação por vez")
	}
	base, err := migrateProjectRoot(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("skills recover: %w", err)
	}
	inspectLock := opts.inspectLock
	if inspectLock == nil {
		inspectLock = skill.InspectOperationLock
	}
	inspectJournal := opts.inspectJournal
	if inspectJournal == nil {
		inspectJournal = skill.InspectTransaction
	}
	releaseFn := opts.releaseStaleFn
	if releaseFn == nil {
		releaseFn = skill.ReleaseStaleProjectLock
	}
	rollback := opts.rollbackFn
	if rollback == nil {
		rollback = skill.RollbackTransactionFromJournal
	}

	switch {
	case opts.releaseStale:
		info, err := releaseFn(base, opts.force)
		if err != nil {
			renderOperationLock(opts.out, info)
			return fmt.Errorf("skills recover: %w", err)
		}
		status := ui.NewStatus(opts.out)
		if !info.Present {
			status.Success("Nenhum lock de processo presente; nada a liberar.")
			return nil
		}
		status.Success(fmt.Sprintf("Lock de processo liberado (dono pid %d veriavelmente morto): %s", info.PID, filepath.Base(info.Path)))
		return nil
	case opts.rollback:
		result, err := rollback(base)
		if err != nil {
			return fmt.Errorf("skills recover: %w", err)
		}
		status := ui.NewStatus(opts.out)
		if result.NoOp {
			status.Success("Nenhum journal de transação presente; nada a recuperar.")
			return nil
		}
		renderRollbackResult(opts.out, result)
		status.Success("Recuperação explícita concluída; journal removido.")
		return nil
	default:
		return renderRecoverInspect(opts.out, base, inspectLock, inspectJournal)
	}
}

func renderRecoverInspect(out io.Writer, base string, inspectLock func(string) (skill.OperationLockInfo, error), inspectJournal func(string) (skill.TransactionInfo, error)) error {
	status := ui.NewStatus(out)
	status.Title("Inspeção de recuperação (somente leitura)")

	lockInfo, err := inspectLock(base)
	if err != nil {
		return fmt.Errorf("skills recover: %w", err)
	}
	renderOperationLock(out, lockInfo)

	journalInfo, err := inspectJournal(base)
	if err != nil {
		return fmt.Errorf("skills recover: %w", err)
	}
	if !journalInfo.Present {
		status.Success("journal de transação: ausente (nenhuma transação pendente)")
		return nil
	}
	status.Warn("journal de transação PRESENTE: " + filepath.Base(journalInfo.Path))
	fmt.Fprintf(out, "  alvo:     %s\n", journalInfo.Target)
	if journalInfo.Stage != "" {
		fmt.Fprintf(out, "  stage:    %s\n", journalInfo.Stage)
	}
	if journalInfo.Backup != "" {
		fmt.Fprintf(out, "  backup:   %s\n", journalInfo.Backup)
	}
	fmt.Fprintf(out, "  hadManifest: %v (payload %d bytes)  hadLock: %v (payload %d bytes)\n",
		journalInfo.HadManifest, journalInfo.OldManifestBytes, journalInfo.HadLock, journalInfo.OldLockBytes)
	if journalInfo.Problem != "" {
		status.Error("payload do journal com problema (fail-closed): " + journalInfo.Problem)
	}
	status.Info("a recuperação automática permanece desativada; use --rollback para a via explícita")
	if journalInfo.Problem != "" {
		return fmt.Errorf("skills recover: journal exige revisão manual: %s", journalInfo.Problem)
	}
	return nil
}

func renderOperationLock(out io.Writer, info skill.OperationLockInfo) {
	if !info.Present {
		fmt.Fprintln(out, "lock de processo: ausente")
		return
	}
	fmt.Fprintf(out, "lock de processo: %s\n", filepath.Base(info.Path))
	if !info.IsDir {
		fmt.Fprintln(out, "  atenção: o caminho existe mas não é um diretório de lock; inspecione manualmente")
		return
	}
	if info.ParseErr != "" {
		fmt.Fprintf(out, "  dono: não verificável (%s)\n", info.ParseErr)
		return
	}
	fmt.Fprintf(out, "  dono: pid %d (token %s…) — liveness: %s\n", info.PID, truncToken(info.Token), info.Liveness)
	if info.Liveness == "cannot-verify" {
		fmt.Fprintln(out, "  esta plataforma não verifica pids; --release-stale-lock exige --force")
	}
}

func renderRollbackResult(out io.Writer, result skill.RollbackResult) {
	status := ui.NewStatus(out)
	if result.RestoredTarget {
		status.Success("diretório alvo restaurado a partir do backup")
	}
	if result.RemovedTarget {
		status.Success("diretório alvo da instalação interrompida removido")
	}
	if result.RestoredManifest {
		status.Success("manifest anterior restaurado (bytes do payload)")
	} else {
		status.Info("manifest anterior não existia; removido o criado pela transação")
	}
	if result.RestoredLock {
		status.Success("lock anterior restaurado (bytes do payload)")
	} else {
		status.Info("lock anterior não existia; removido o criado pela transação")
	}
	if result.RemovedStage {
		status.Success("stage com marcador de propriedade removido")
	}
	for _, leftover := range result.LeftOvers {
		status.Warn("diretório stage SEM marcador de propriedade deixado no lugar para revisão: " + leftover)
	}
}

func truncToken(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}
