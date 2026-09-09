package skill

import (
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"

	"oroborus.dev/orotools/internal/manifest"
)

// UpdateOptions tune the update engine (skills-manager FR-25, FR-27).
type UpdateOptions struct {
	Force  bool // replace drifted skills, discarding local changes
	DryRun bool // decide and report without touching anything
}

// UpdateDecision classifies one lock entry against its upstream (FR-28).
type UpdateDecision string

const (
	UpdateCurrent   UpdateDecision = "atual"
	UpdateAvailable UpdateDecision = "update"
	UpdateBlocked   UpdateDecision = "bloqueada"
	UpdateFailed    UpdateDecision = "erro"
)

// UpdateResult is the structured outcome for one lock entry.
type UpdateResult struct {
	ID       string
	Name     string
	From     string
	To       string
	Decision UpdateDecision
	Detail   string // explanation for current-warnings, blocked and failed items
}

// UpdateReport aggregates best-effort results in lock ID order (FR-28).
type UpdateReport struct {
	Results []UpdateResult
	Updated int
	Current int
	Blocked int
	Failed  int
}

// UpdateInput carries the resolved catalogs and migration state the engine
// compares the lock against.
type UpdateInput struct {
	Bundled   []Candidate       // recipe candidates (authoritative, embedded)
	Remote    []Candidate       // GitHub candidates; may be nil when offline
	Migration []MigrationResult // lazy migration of legacy bundled skills
}

// Update walks every lock entry (skills.external never reaches the lock —
// FR-22), compares it with the upstream catalog and applies updates per skill
// atomically, best-effort (FR-28). Dry-run decides without mutating (FR-27).
func (s Service) Update(m *manifest.Manifest, lock *Lock, in UpdateInput, opts UpdateOptions) (UpdateReport, error) {
	var report UpdateReport
	if lock == nil {
		return report, fmt.Errorf("update: lock ausente")
	}
	byID := make(map[string]Candidate, len(in.Bundled)+len(in.Remote))
	for _, cand := range append(append([]Candidate(nil), in.Bundled...), in.Remote...) {
		byID[cand.ID] = cand
	}

	for _, entry := range lock.Skills {
		res := s.updateOne(m, lock, entry, byID, opts)
		report.Results = append(report.Results, res)
		switch res.Decision {
		case UpdateAvailable:
			report.Updated++
		case UpdateCurrent:
			report.Current++
		case UpdateBlocked:
			report.Blocked++
		case UpdateFailed:
			report.Failed++
		}
	}

	// Divergent legacy bundled skills are not in the lock; only --force may
	// replace and register them (FR-20, FR-25).
	for _, mig := range in.Migration {
		if mig.State != MigrationDiverged {
			continue
		}
		cand, ok := byID[mig.ID]
		if !ok {
			report.Results = append(report.Results, UpdateResult{
				ID: mig.ID, Name: mig.Name, Decision: UpdateBlocked,
				Detail: "não gerenciada (modificada); origem ausente no catálogo",
			})
			report.Blocked++
			continue
		}
		res := s.forceRegister(m, lock, cand, opts)
		report.Results = append(report.Results, res)
		switch res.Decision {
		case UpdateAvailable:
			report.Updated++
		case UpdateBlocked:
			report.Blocked++
		case UpdateCurrent:
			report.Current++
		default:
			report.Failed++
		}
	}
	return report, nil
}

func (s Service) updateOne(m *manifest.Manifest, lock *Lock, entry LockEntry, byID map[string]Candidate, opts UpdateOptions) UpdateResult {
	res := UpdateResult{ID: entry.ID, Name: entry.Name, From: entry.Version}

	// FR-26: an agent change moves the derived target; never move or duplicate
	// installed skills between agents in this feature.
	targetRel, err := s.TargetFor(entry.Name)
	if err != nil {
		res.Decision = UpdateBlocked
		res.Detail = fmt.Sprintf("agent inválido: %v", err)
		return res
	}
	if targetRel != entry.Target {
		res.Decision = UpdateBlocked
		res.Detail = fmt.Sprintf("manifest.ai.agent mudou; destino seria %s, mas a skill vive em %s (migração entre agents não é suportada)", targetRel, entry.Target)
		return res
	}

	cand, ok := byID[entry.ID]
	if !ok {
		res.Decision = UpdateFailed
		res.Detail = "referência ausente no catálogo (o destino não é removido)"
		return res
	}

	installed, err := semver.StrictNewVersion(entry.Version)
	if err != nil {
		res.Decision = UpdateFailed
		res.Detail = fmt.Sprintf("versão no lock inválida: %v", err)
		return res
	}
	cmp := cand.Version.Compare(installed)

	// FR-19/FR-25 (review 2026-09-09): drift and missing-target detection run
	// for every owned entry, independent of the upstream version — a modified
	// or deleted skill must never pass as "current".
	destDigest, derr := Digest(os.DirFS(s.Base), entry.Target)
	drifted := derr == nil && destDigest != entry.ContentSHA256
	missing := derr != nil

	// FR-23/FR-24 (review 2026-09-09, round 2): a published downgrade is
	// never installed — not even with --force, which authorizes discarding
	// drift, not downgrading.
	if cmp < 0 {
		switch {
		case missing:
			res.Decision = UpdateBlocked
			res.Detail = fmt.Sprintf("destino ausente e upstream só publica %s (menor); downgrade não é instalado (FR-23/FR-24)", cand.Version.String())
		case drifted:
			res.Decision = UpdateBlocked
			res.Detail = fmt.Sprintf("alterações locais detectadas e upstream só publica %s (menor); downgrade não é instalado (FR-23/FR-24)", cand.Version.String())
		default:
			res.Decision = UpdateCurrent
			res.Detail = fmt.Sprintf("upstream publicou %s (menor que a instalada); downgrade ignorado", cand.Version.String())
		}
		return res
	}

	// FR-24: bundled content changed without a version bump is a packaging
	// error, independent of local drift.
	if cmp == 0 && cand.Source == SourceRecipe {
		sourceDigest, derr := Digest(cand.Tree.FS, cand.Tree.Root)
		if derr != nil {
			res.Decision = UpdateFailed
			res.Detail = fmt.Sprintf("digest da origem: %v", derr)
			return res
		}
		if sourceDigest != entry.ContentSHA256 {
			res.Decision = UpdateFailed
			res.Detail = "empacotamento: conteúdo da skill mudou sem bump de versão"
			return res
		}
	}

	if drifted || missing {
		res.To = cand.Version.String()
		if !opts.Force {
			res.Decision = UpdateBlocked
			res.To = ""
			if missing {
				res.Detail = "destino ausente; use --force para reinstalar"
			} else {
				res.Detail = "alterações locais detectadas; use --force para substituir (as mudanças serão perdidas)"
			}
			return res
		}
		switch {
		case missing:
			res.Detail = "destino ausente; reinstalada (--force)"
		case cmp == 0:
			res.Detail = "restaurada (--force; alterações locais descartadas)"
		default:
			res.Detail = "alterações locais serão descartadas (--force)"
		}
		res.Decision = UpdateAvailable
		if opts.DryRun {
			return res
		}
		if err := s.applyUpdate(m, lock, cand, entry); err != nil {
			res.Decision = UpdateFailed
			res.To = ""
			res.Detail = err.Error()
			return res
		}
		return res
	}

	if cmp == 0 {
		res.Decision = UpdateCurrent
		if cand.Version.Metadata() != "" || installed.Metadata() != "" {
			res.Detail = "diferença apenas de build metadata não força atualização"
		}
		return res
	}

	// cmp > 0: an update is available with no local drift.
	res.To = cand.Version.String()
	res.Decision = UpdateAvailable
	if opts.DryRun {
		return res
	}
	if err := s.applyUpdate(m, lock, cand, entry); err != nil {
		res.Decision = UpdateFailed
		res.To = ""
		res.Detail = err.Error()
		return res
	}
	return res
}

// applyUpdate stages the candidate and swaps it in for an owned target
// (skills-manager design transaction, FR-29: the lock is written only after
// the swap; a persist failure restores the previous directory).
func (s Service) applyUpdate(m *manifest.Manifest, lock *Lock, cand Candidate, entry LockEntry) error {
	staging, stagingDigest, err := s.stage(cand)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	return s.replace(m, lock, cand, entry, staging, stagingDigest, nil)
}

// forceRegister replaces a divergent legacy bundled skill with the
// authoritative content and registers it in the lock (--force only).
func (s Service) forceRegister(m *manifest.Manifest, lock *Lock, cand Candidate, opts UpdateOptions) UpdateResult {
	res := UpdateResult{ID: cand.ID, Name: cand.Name, From: "local"}
	if !opts.Force {
		res.Decision = UpdateBlocked
		res.Detail = "não gerenciada (modificada); use --force para substituir e registrar"
		return res
	}
	if opts.DryRun {
		res.Decision = UpdateAvailable
		res.To = cand.Version.String()
		res.Detail = "será substituída e registrada (--force)"
		return res
	}
	targetRel, err := s.TargetFor(cand.Name)
	if err != nil {
		res.Decision = UpdateFailed
		res.Detail = err.Error()
		return res
	}
	staging, stagingDigest, err := s.stage(cand)
	if err != nil {
		res.Decision = UpdateFailed
		res.Detail = fmt.Sprintf("stage: %v", err)
		return res
	}
	entry := LockEntry{
		ID:        cand.ID,
		Name:      cand.Name,
		Source:    string(cand.Source),
		SourceRef: cand.SourceRef,
		Path:      cand.Path,
		Target:    targetRel,
	}
	if err := s.replace(m, lock, cand, entry, staging, stagingDigest, nil); err != nil {
		res.Decision = UpdateFailed
		res.Detail = err.Error()
		return res
	}
	res.Decision = UpdateAvailable
	res.To = cand.Version.String()
	res.Detail = "substituída e registrada no lock (--force)"
	return res
}
