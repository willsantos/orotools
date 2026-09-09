package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/skill"
	"oroborus.dev/orotools/internal/ui"
	"oroborus.dev/orotools/recipes"
)

// skillsOptions carries the wizard inputs and the injection points used by
// tests (GitHub/filesystem/terminal are injectable — NFR-2).
type skillsOptions struct {
	manifestPath string
	out          io.Writer
	errOut       io.Writer
	stdin        io.Reader

	// recipeCatalog overrides the bundled catalog built from the manifest
	// recipe. loadGitHub overrides the remote catalog loader; it returns a
	// cleanup the caller must always invoke.
	recipeCatalog skill.CatalogProvider
	loadGitHub    func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error)
	// selector overrides the interactive multi-select. stdinTTY decides the
	// interactivity check; nil defers to TTY detection on stdin.
	selector skillSelector
	stdinTTY *bool
}

// skillOption is one selectable entry of the wizard.
type skillOption struct {
	ID    string
	Label string
}

// skillSelector asks the user to pick zero or more skills, returning the
// selected IDs. An error means the wizard was cancelled.
type skillSelector interface {
	Select(title string, options []skillOption) ([]string, error)
}

// skillsContext bundles the resolved project state shared by `oro skills` and
// `oro skills update`. Loading it performs no mutation.
type skillsContext struct {
	svc  skill.Service
	m    *manifest.Manifest
	lock *skill.Lock
}

func loadSkillsContext(manifestPath string) (skillsContext, error) {
	manifestAbs, err := filepath.Abs(manifestPath)
	if err != nil {
		return skillsContext{}, fmt.Errorf("resolver manifest: %w", err)
	}
	m, err := manifest.Read(manifestAbs)
	if err != nil {
		return skillsContext{}, err
	}
	base := filepath.Dir(manifestAbs)
	svc := skill.Service{
		Base:         base,
		Agent:        skill.AgentFromManifest(m),
		ManifestPath: manifestAbs,
		LockPath:     filepath.Join(base, skill.LockDir, skill.LockFilename),
	}
	lock, err := skill.ReadLock(svc.LockPath)
	if errors.Is(err, skill.ErrLockNotFound) {
		lock = skill.NewLock()
	} else if err != nil {
		return skillsContext{}, err
	}
	return skillsContext{svc: svc, m: m, lock: lock}, nil
}

// runSkills implements `oro skills`: shows installed skills, migrates legacy
// bundled skills into the lock and interactively installs the selection
// (skills-manager FR-1, FR-11, FR-15, FR-16).
func runSkills(opts skillsOptions) error {
	out := opts.out
	status := ui.NewStatus(out)

	ctx, err := loadSkillsContext(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	svc, lock := ctx.svc, ctx.lock

	// The wizard is interactive by nature (FR-4); the non-interactive path is
	// `oro skills update`. Checked before any mutation.
	if !isInteractiveStdin(opts) {
		return fmt.Errorf("skills: stdin não interativo; o wizard precisa de um terminal (use `oro skills update` para operação não interativa)")
	}
	svc.CleanOrphans()

	// Bundled catalog: errors are fatal packaging problems (FR-7).
	bundled, err := loadBundledCatalog(opts, ctx.m)
	if err != nil {
		return fmt.Errorf("skills: %w", err)
	}

	// Remote catalog: degradation to warning keeps bundled usable (FR-10).
	remote, remoteWarns, cleanupRemote, err := loadRemoteCatalog(opts)
	if cleanupRemote != nil {
		defer cleanupRemote()
	}
	if err != nil {
		status.Warn(fmt.Sprintf("catálogo %s indisponível: %v", skill.GitHubCatalogRef, err))
	}

	// Lazy migration of legacy bundled skills (FR-20): adopts intact targets
	// into the lock before classifying what is installed vs available.
	migration, err := svc.MigrateBundled(lock, bundled)
	if err != nil {
		return fmt.Errorf("skills: %w", err)
	}

	candidates := append(append([]skill.Candidate(nil), bundled...), remote...)
	available := make([]skill.Candidate, 0, len(candidates))
	for _, cand := range candidates {
		if _, installed := lock.Lookup(cand.ID); !installed {
			available = append(available, cand)
		}
	}

	// Deterministic presentation already guaranteed by the catalogs.
	for _, w := range remoteWarns {
		status.Warn(fmt.Sprintf("%s ignorada: %s", w.SkillID, w.Message))
	}

	renderInstalled(out, lock)
	renderUnmanaged(out, migration)

	if len(available) == 0 {
		status.Success("Todas as skills do catálogo já estão instaladas; nada a fazer.")
		return nil
	}
	if dups := skill.DestinationCollisions(available); len(dups) > 0 {
		return fmt.Errorf("skills: conflito de destino entre candidatas: %v", dups)
	}

	options := make([]skillOption, 0, len(available))
	for _, cand := range available {
		options = append(options, skillOption{
			ID:    cand.ID,
			Label: fmt.Sprintf("%s %s %s — %s", cand.Name, cand.Version.String(), sourceLabel(string(cand.Source), cand.SourceRef), cand.Description),
		})
	}
	selectedIDs, err := selectSkills(opts, "Selecione as skills para instalar", options)
	if err != nil {
		status.Info("cancelado")
		return nil
	}
	if len(selectedIDs) == 0 {
		status.Success("Nenhuma skill selecionada; nada a fazer.")
		return nil
	}
	byID := make(map[string]skill.Candidate, len(candidates))
	for _, cand := range candidates {
		byID[cand.ID] = cand
	}

	installedCount := 0
	var failures []string
	for _, id := range selectedIDs {
		cand, ok := byID[id]
		if !ok {
			failures = append(failures, fmt.Sprintf("%s: candidata não encontrada", id))
			continue
		}
		res, err := svc.Install(ctx.m, lock, cand)
		switch {
		case err != nil:
			status.Error(fmt.Sprintf("%s: %v", cand.Name, err))
			failures = append(failures, cand.ID)
		case res.Already:
			status.Info(fmt.Sprintf("%s %s já instalada (conteúdo idêntico)", res.Name, res.Version))
			installedCount++
		default:
			status.Success(fmt.Sprintf("%s %s instalada em %s", res.Name, res.Version, res.Target))
			installedCount++
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("skills: %d instalação(ões) falharam", len(failures))
	}
	status.Success(fmt.Sprintf("%d skill(s) instalada(s).", installedCount))
	return nil
}

func loadBundledCatalog(opts skillsOptions, m *manifest.Manifest) ([]skill.Candidate, error) {
	if opts.recipeCatalog != nil {
		cands, _, err := opts.recipeCatalog.List(context.Background())
		return cands, err
	}
	return loadBundledFromManifest(m)
}

func loadBundledFromManifest(m *manifest.Manifest) ([]skill.Candidate, error) {
	fsys, err := recipes.Open(m.Recipe.Name)
	if err != nil {
		return nil, err
	}
	r, err := recipe.Load(fsys, recipes.RecipeFile)
	if err != nil {
		return nil, err
	}
	cands, _, err := skill.RecipeCatalog{RecipeName: m.Recipe.Name, FS: fsys, Refs: r.Skills.Bundled}.List(context.Background())
	return cands, err
}

func loadRemoteCatalog(opts skillsOptions) ([]skill.Candidate, []skill.Warning, func(), error) {
	if opts.loadGitHub != nil {
		return opts.loadGitHub(context.Background())
	}
	noCleanup := func() {}
	client := skill.HTTPGitHubClient{Token: os.Getenv("GITHUB_TOKEN")}
	rev, err := client.Resolve(context.Background())
	if err != nil {
		return nil, nil, noCleanup, err
	}
	snap, err := client.Snapshot(context.Background(), rev)
	if err != nil {
		return nil, nil, noCleanup, err
	}
	cands, warns, err := skill.GitHubCatalog{Snapshot: snap}.List(context.Background())
	return cands, warns, snap.Cleanup, err
}

// renderInstalled prints the installed skills from the lock (FR-11).
func renderInstalled(out io.Writer, lock *skill.Lock) {
	if len(lock.Skills) == 0 {
		return
	}
	fmt.Fprintln(out, "Skills já instaladas")
	marker := listMarker(out)
	for _, e := range lock.Skills {
		fmt.Fprintf(out, "  %c %-28s %-8s %s\n", marker, e.Name, e.Version, sourceLabel(e.Source, e.SourceRef))
	}
	fmt.Fprintln(out)
}

// renderUnmanaged reports legacy bundled skills left out of the lock because
// their target diverged (FR-20).
func renderUnmanaged(out io.Writer, migration []skill.MigrationResult) {
	var shown bool
	for _, res := range migration {
		if res.State != skill.MigrationDiverged {
			continue
		}
		if !shown {
			fmt.Fprintln(out, "Skills não gerenciadas (conteúdo local diverge da origem)")
			shown = true
		}
		fmt.Fprintf(out, "  ! %-28s %s (use `oro skills update --force` para substituir)\n", res.Name, res.Target)
	}
	if shown {
		fmt.Fprintln(out)
	}
}

// listMarker picks the bullet for plain list lines based on TTY rendering.
func listMarker(out io.Writer) rune {
	if ui.IsTTY(out) {
		return '✓'
	}
	return '+'
}

// skillsUpdateOptions carries the `oro skills update` inputs and test
// injection points.
type skillsUpdateOptions struct {
	manifestPath string
	force        bool
	dryRun       bool
	out          io.Writer
	errOut       io.Writer

	recipeCatalog skill.CatalogProvider
	loadGitHub    func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error)
}

// runSkillsUpdate implements `oro skills update`: compares every lock entry
// with the catalogs and applies updates best-effort, with a deterministic
// report and an aggregated exit code (skills-manager FR-1, FR-27, FR-28).
func runSkillsUpdate(opts skillsUpdateOptions) error {
	out := opts.out
	status := ui.NewStatus(out)

	ctx, err := loadSkillsContext(opts.manifestPath)
	if err != nil {
		return fmt.Errorf("skills update: %w", err)
	}
	svc, lock := ctx.svc, ctx.lock
	svc.CleanOrphans()

	// Bundled catalog: fatal on packaging problems (FR-7).
	bundled, err := loadBundledCatalog(skillsOptions{recipeCatalog: opts.recipeCatalog}, ctx.m)
	if err != nil {
		return fmt.Errorf("skills update: %w", err)
	}

	// Remote failure degrades: bundled entries still processed, managed ones
	// fail per-item in the report (FR-10, decision 11).
	remote, remoteWarns, cleanupRemote, err := loadRemoteCatalog(skillsOptions{loadGitHub: opts.loadGitHub})
	defer cleanupRemote()
	if err != nil {
		status.Warn(fmt.Sprintf("catálogo %s indisponível: %v", skill.GitHubCatalogRef, err))
		remote = nil
	}
	for _, w := range remoteWarns {
		status.Warn(fmt.Sprintf("%s ignorada: %s", w.SkillID, w.Message))
	}

	migration, err := svc.MigrateBundled(lock, bundled)
	if err != nil {
		return fmt.Errorf("skills update: %w", err)
	}

	report, err := svc.Update(ctx.m, lock, skill.UpdateInput{
		Bundled:   bundled,
		Remote:    remote,
		Migration: migration,
	}, skill.UpdateOptions{Force: opts.force, DryRun: opts.dryRun})
	if err != nil {
		return fmt.Errorf("skills update: %w", err)
	}

	renderUpdateReport(out, report, opts.dryRun)
	if len(report.Results) == 0 {
		status.Info("Nenhuma skill gerenciada para atualizar.")
		return nil
	}
	if report.Blocked+report.Failed > 0 {
		return fmt.Errorf("skills update: %d bloqueada(s), %d falha(s)", report.Blocked, report.Failed)
	}
	return nil
}

// renderUpdateReport prints the deterministic per-skill report (FR-28):
// update ✓, current =, blocked !, failed ✗ — degrading without a TTY.
func renderUpdateReport(out io.Writer, report skill.UpdateReport, dryRun bool) {
	good, cur, warn, bad := '✓', '=', '!', '✗'
	if !ui.IsTTY(out) {
		good, cur, warn, bad = '+', '=', '!', 'x'
	}
	for _, r := range report.Results {
		name := r.Name
		switch r.Decision {
		case skill.UpdateAvailable:
			line := fmt.Sprintf("%c %-28s %s → %s", good, name, r.From, r.To)
			if r.Detail != "" {
				line += fmt.Sprintf(" (%s)", r.Detail)
			}
			fmt.Fprintln(out, line)
		case skill.UpdateCurrent:
			line := fmt.Sprintf("%c %-28s %s (atual)", cur, name, r.From)
			if r.Detail != "" {
				line += fmt.Sprintf(": %s", r.Detail)
			}
			fmt.Fprintln(out, line)
		case skill.UpdateBlocked:
			fmt.Fprintf(out, "%c %-28s %s — %s\n", warn, name, r.From, r.Detail)
		default:
			fmt.Fprintf(out, "%c %-28s %s — %s\n", bad, name, r.From, r.Detail)
		}
	}
	prefix := ""
	if dryRun {
		prefix = "dry-run: "
	}
	fmt.Fprintf(out, "%s%d atualizada(s), %d atual(is), %d bloqueada(s), %d falha(s)\n",
		prefix, report.Updated, report.Current, report.Blocked, report.Failed)
}

func selectSkills(opts skillsOptions, title string, options []skillOption) ([]string, error) {
	sel := opts.selector
	if sel == nil {
		sel = huhSkillSelector{}
	}
	return sel.Select(title, options)
}

func isInteractiveStdin(opts skillsOptions) bool {
	if opts.stdinTTY != nil {
		return *opts.stdinTTY
	}
	f, ok := opts.stdin.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// huhSkillSelector is the production selector backed by the huh wizard.
type huhSkillSelector struct{}

func (huhSkillSelector) Select(title string, options []skillOption) ([]string, error) {
	huhOpts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		huhOpts = append(huhOpts, huh.NewOption(o.Label, o.ID))
	}
	var selected []string
	field := huh.NewMultiSelect[string]().
		Title(title).
		Options(huhOpts...).
		Value(&selected)
	if err := huh.NewForm(huh.NewGroup(field)).Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

func sourceLabel(source, sourceRef string) string {
	return source + ":" + sourceRef
}
