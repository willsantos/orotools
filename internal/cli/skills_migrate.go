package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/skill"
	"oroborus.dev/orotools/internal/ui"
)

// skillsMigrateOptions carries `oro skills migrate` inputs.
type skillsMigrateOptions struct {
	manifestPath string
	apply        bool
	out          io.Writer
	errOut       io.Writer
}

func newSkillsMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Relatório (e conversão opt-in) dos locks legados JSON v2",
		Long: `Inventaria os locks legados do projeto — .agents/.skill-lock.json
(formato JSON v2 do antigo instalador Python) — e produz um relatório
SOMENTE LEITURA: para cada reivindicação (skill × agente) mostra o alvo,
a origem registrada, o hash legado gravado, o hash legado RECALCULADO a
partir dos bytes reais do disco e o digest canônico
(orotools-tree-sha256-v1). Os dois algoritmos nunca são comparados entre
si: a divergência do hash legado é modificação local, nunca autorização
para substituir ou baixar conteúdo.

Decisões por entrada:
  migratable        alvo existe, digest recomputado; origem registrada
                    como desconhecida quando não verificável
  drift-modified    migratable com modificação local detectada
  already-present   lock canônico já tem entrada idêntica
  conflict          alvo disputado (outro claim legado ou o lock canônico)
  missing           diretório alvo ausente — nada é inventado
  unsafe            alvo escapa do projeto ou usa symlink (convenções do motor)
  unversioned       sem versão SemVer estrita recuperável; nada é inventado
  unmappable        entrada fora do contrato (agent desconhecido, global...)

--apply converte (opt-in, atômico): exige o lock de processo do projeto,
grava cópias de backup em .orotools/legacy-backup/, escreve
.orotools/skills.lock.yaml (v1) com as entradas migratáveis, preserva os
arquivos legados no lugar e registra .orotools/.legacy-lock-migration.json.
Qualquer conflito aborta sem escrever nada. Repetir é idempotente.

Importante: o protocolo de processo v1 (oro skills process) permanece
inalterado — a migração é exclusiva da CLI e não reescreve locks via
protocolo. Um binário antigo que desconhece o marcador NÃO é impedido
tecnicamente por ele; isso exige bloqueio operacional ou versão mínima.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manifestPath, _ := cmd.Flags().GetString("manifest")
			apply, _ := cmd.Flags().GetBool("apply")
			return runSkillsMigrate(skillsMigrateOptions{
				manifestPath: manifestPath,
				apply:        apply,
				out:          cmd.OutOrStdout(),
				errOut:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().String("manifest", manifestFilename, "caminho para o orotools.yaml (a raiz do projeto é o diretório dele)")
	cmd.Flags().Bool("apply", false, "aplica a conversão (o default é somente leitura)")
	return cmd
}

func runSkillsMigrate(opts skillsMigrateOptions) error {
	base, err := migrateProjectRoot(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("skills migrate: %w", err)
	}
	status := ui.NewStatus(opts.out)
	report, err := skill.InspectLegacyLocks(base)
	if err != nil {
		return fmt.Errorf("skills migrate: %w", err)
	}
	renderLegacyReport(opts.out, report)

	if !opts.apply {
		if report.AlreadyMigrated {
			status.Success("Migração já aplicada neste projeto (marcador presente); relatório apenas informativo.")
			return nil
		}
		switch {
		case len(report.LegacyFiles) == 0:
			status.Success("Nenhum lock legado encontrado; nada a migrar.")
		case report.ApplyBlocked() != "":
			status.Warn("A conversão está bloqueada: " + report.ApplyBlocked())
		default:
			status.Info("Relatório somente leitura; use --apply para converter (opt-in, atômico).")
		}
		return nil
	}

	prior := report
	result, err := skill.ApplyLegacyMigration(base, prior)
	if err != nil {
		return fmt.Errorf("skills migrate --apply: %w", err)
	}
	if result.NoOp {
		status.Success("Nada a fazer: migração já aplicada ou nenhum lock legado encontrado.")
		return nil
	}
	for _, f := range result.Skipped {
		status.Warn(fmt.Sprintf("ignorada %s@%s (%s): %s", f.Name, f.Target, f.Decision, f.Detail))
	}
	status.Success(fmt.Sprintf("Lock canônico escrito: %s (%d entrada(s) nova(s), %d já presente(s)); arquivos legados preservados; backups em .orotools/%s.",
		filepath.ToSlash(filepath.Join(skill.LockDir, skill.LockFilename)), result.Written, result.AlreadyPresent, skill.LegacyBackupDirRel))
	if len(result.Skipped) > 0 {
		return fmt.Errorf("skills migrate --apply: conversão incompleta: %d entrada(s) ignorada(s) — resolva e execute novamente", len(result.Skipped))
	}
	return nil
}

// migrateProjectRoot resolves the project root from --manifest without
// requiring the manifest to exist: legacy-only projects have no orotools.yaml.
func migrateProjectRoot(manifestPath string) (string, error) {
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return "", fmt.Errorf("resolver manifest: %w", err)
	}
	return filepath.Dir(abs), nil
}

// renderLegacyReport prints the deterministic read-only survey (non-TTY uses
// ASCII markers, mirroring the update report).
func renderLegacyReport(out io.Writer, report *skill.LegacyMigrationReport) {
	good, drift, warn, bad := '+', '~', '!', 'x'
	status := ui.NewStatus(out)
	status.Title("Relatório de migração de locks legados (somente leitura)")
	fmt.Fprintf(out, "raiz do projeto: %s\n", report.ProjectRoot)
	fmt.Fprintf(out, "lock canônico %s: %s\n", filepath.ToSlash(filepath.Join(skill.LockDir, skill.LockFilename)), report.Canonical)
	if report.AlreadyMigrated {
		status.Success(fmt.Sprintf("marcador de migração presente: %s (reexecutar --apply é no-op)", filepath.ToSlash(filepath.Join(skill.LockDir, skill.LegacyMigrationMarkerRel))))
	}
	for _, blocked := range report.BlockedPaths {
		status.Warn(fmt.Sprintf("arquivo reconhecido e RECUSADO (formato legado não suportado): %s", blocked))
	}
	for _, problem := range report.LegacyProblems {
		status.Error("lock legado ilegível: " + problem)
	}
	if len(report.Findings) == 0 {
		if len(report.LegacyFiles) == 0 {
			fmt.Fprintln(out, "nenhum lock legado (.agents/.skill-lock.json) encontrado")
		}
		fmt.Fprintln(out, "resumo: 0 entradas legadas")
		return
	}
	fmt.Fprintf(out, "locks legados: %s\n", strings.Join(report.LegacyFiles, ", "))
	for _, f := range report.Findings {
		line := fmt.Sprintf("%c %-32s %-34s origem=%s", markerFor(f.Decision, good, drift, warn, bad), f.Name, "alvo="+f.Target, f.Origin)
		switch f.Decision {
		case skill.LegacyMigratable, skill.LegacyDriftModified, skill.LegacyAlreadyPresent:
			line += fmt.Sprintf(" versao=%s", f.Version)
			line += fmt.Sprintf(" legado=%s", shortHash(f.RecordedHash))
			if !f.LegacyHashVerified {
				line += fmt.Sprintf(" (recalculado=%s: divergente)", shortHash(f.RecomputedHash))
			}
			line += fmt.Sprintf(" canônico=%s (%s)", f.CanonicalDigest, f.CanonicalAlgorithm)
		case skill.LegacyConflict, skill.LegacyMissing, skill.LegacyUnsafe, skill.LegacyUnversioned, skill.LegacyUnmappable:
			if f.RecordedHash != "" {
				line += fmt.Sprintf(" legado gravado=%s (%s)", shortHash(f.RecordedHash), f.RecordedAlgorithm)
			}
		}
		if f.Detail != "" {
			line += " — " + f.Detail
		}
		fmt.Fprintf(out, "%s [%s]\n", line, f.Decision)
	}
	counts := decisionCounts(report)
	fmt.Fprintf(out, "resumo: %s\n", strings.Join(counts, ", "))
}

func markerFor(decision skill.LegacyMigrationDecision, good, drift, warn, bad rune) rune {
	switch decision {
	case skill.LegacyMigratable, skill.LegacyAlreadyPresent:
		return good
	case skill.LegacyDriftModified:
		return drift
	case skill.LegacyConflict, skill.LegacyMissing, skill.LegacyUnsafe, skill.LegacyUnversioned, skill.LegacyUnmappable:
		return warn
	default:
		return bad
	}
}

func decisionCounts(report *skill.LegacyMigrationReport) []string {
	order := []skill.LegacyMigrationDecision{
		skill.LegacyMigratable, skill.LegacyDriftModified, skill.LegacyAlreadyPresent,
		skill.LegacyConflict, skill.LegacyMissing, skill.LegacyUnsafe,
		skill.LegacyUnversioned, skill.LegacyUnmappable,
	}
	counts := make([]string, 0, len(order))
	for _, decision := range order {
		n := 0
		for _, f := range report.Findings {
			if f.Decision == decision {
				n++
			}
		}
		if n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, decision))
		}
	}
	if len(counts) == 0 {
		counts = append(counts, "0 entradas")
	}
	return counts
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12] + "…"
}
