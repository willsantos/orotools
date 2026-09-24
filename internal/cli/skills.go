package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	manifestPath     string
	manifestExplicit bool
	agentFlag        string
	out              io.Writer
	errOut           io.Writer
	stdin            io.Reader

	// recipeCatalog overrides the bundled catalog built from the manifest
	// recipe. loadGitHub overrides the remote catalog loader; it returns a
	// cleanup the caller must always invoke.
	recipeCatalog skill.CatalogProvider
	loadGitHub    func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error)
	// selector overrides the interactive multi-select. stdinTTY decides the
	// interactivity check; nil defers to TTY detection on stdin. pickAgents
	// overrides the agent multi-select of the ambiguous ad-hoc case.
	selector   skillSelector
	stdinTTY   *bool
	pickAgents func(candidates []string) ([]string, error)
}

// skillOption is one selectable entry of the wizard. Beyond the legacy flat
// Label (huh fallback), the metadata fields feed the full-screen picker:
// grouping by origin, description line and detail panel.
type skillOption struct {
	ID          string
	Name        string
	Label       string
	Description string
	Version     string
	Source      string // recipe|github
	SourceRef   string // recipe name or repo slug
}

// skillSelector asks the user to pick zero or more skills, returning the
// selected IDs. An error means the wizard was cancelled.
type skillSelector interface {
	Select(title string, options []skillOption) ([]string, error)
}

// skillsContext bundles the resolved project state shared by `oro skills` and
// `oro skills update`. Loading it performs no mutation. In ad-hoc mode (no
// orotools.yaml) the manifest is nil and only the GitHub catalog applies;
// agents carries the resolved destinations (one entry for the manifest flow,
// one per selected agent for the wizard in ad-hoc mode).
type skillsContext struct {
	svc       skill.Service
	m         *manifest.Manifest // nil em modo ad-hoc
	lock      *skill.Lock
	adHoc     bool
	agents    []string // destinos de instalação resolvidos (≥1 no wizard)
	agentNote string   // info sobre a escolha de agent, exibida pelo chamador
}

// contextOptions parametrizes the context resolution for the two skills
// commands: the wizard is interactive (may ask the agents), update is not.
type contextOptions struct {
	manifestPath     string
	manifestExplicit bool // --manifest passado explicitamente
	agentFlag        string
	interactive      bool
	pickAgents       func(candidates []string) ([]string, error)
}

func loadSkillsContext(o contextOptions) (skillsContext, error) {
	manifestAbs, err := filepath.Abs(o.manifestPath)
	if err != nil {
		return skillsContext{}, fmt.Errorf("resolver manifest: %w", err)
	}
	m, err := manifest.Read(manifestAbs)
	switch {
	case err == nil:
		if o.agentFlag != "" {
			return skillsContext{}, fmt.Errorf("--agent só pode ser usado sem orotools.yaml (o agent vem de ai.agent)")
		}
		base := filepath.Dir(manifestAbs)
		svc := skill.Service{
			Base:         base,
			Agent:        skill.AgentFromManifest(m),
			ManifestPath: manifestAbs,
			LockPath:     filepath.Join(base, skill.LockDir, skill.LockFilename),
		}
		lock, err := readLockOrEmpty(svc.LockPath)
		if err != nil {
			return skillsContext{}, err
		}
		return skillsContext{svc: svc, m: m, lock: lock, agents: []string{svc.Agent}}, nil
	case errors.Is(err, fs.ErrNotExist) && !o.manifestExplicit:
		// Ad-hoc mode (emenda FR-2/decisão 12): operate without a manifest,
		// locating the agent skills directories directly.
		return loadAdHocContext(manifestAbs, o)
	default:
		if errors.Is(err, fs.ErrNotExist) {
			return skillsContext{}, fmt.Errorf("nenhum projeto Oro encontrado: %s não existe — execute dentro de um projeto (ou aponte --manifest para o orotools.yaml dele)", manifestAbs)
		}
		return skillsContext{}, err
	}
}

func readLockOrEmpty(path string) (*skill.Lock, error) {
	lock, err := skill.ReadLock(path)
	if errors.Is(err, skill.ErrLockNotFound) {
		return skill.NewLock(), nil
	}
	return lock, err
}

// loadAdHocContext resolves the agent without a manifest: --agent wins, then
// the update flow derives each entry from its lock target, then the wizard
// detects existing skills directories (asking when ambiguous), defaulting to
// opencode when none exists.
func loadAdHocContext(manifestAbs string, o contextOptions) (skillsContext, error) {
	base := filepath.Dir(manifestAbs)
	lock, err := readLockOrEmpty(filepath.Join(base, skill.LockDir, skill.LockFilename))
	if err != nil {
		return skillsContext{}, err
	}
	ctx := skillsContext{
		m:     nil,
		lock:  lock,
		adHoc: true,
		svc: skill.Service{
			Base:         base,
			ManifestPath: manifestAbs,
			LockPath:     filepath.Join(base, skill.LockDir, skill.LockFilename),
		},
	}
	switch {
	case o.agentFlag != "":
		if _, err := skill.SkillsDir(o.agentFlag); err != nil {
			return skillsContext{}, fmt.Errorf("--agent inválido: %w", err)
		}
		// Update ad-hoc: o agent de cada entrada vem do target no lock; a flag
		// não altera o processamento e a nota explica (skills-multi-agent FR-16).
		if !o.interactive && len(lock.Skills) > 0 {
			ctx.agentNote = "sem orotools.yaml: update deriva os agents dos targets do lock (--agent ignorado)"
		}
		ctx.svc.Agent = o.agentFlag
		ctx.agents = []string{o.agentFlag}
		return ctx, nil
	case !o.interactive:
		// Update: cada entrada do lock carrega o próprio agent (updateOne deriva
		// do target); sem lock não há destino a resolver.
		ctx.svc.Agent = "opencode"
		return ctx, nil
	default:
		var found []string
		for _, agent := range skill.AllAgents {
			dir, err := skill.SkillsDir(agent)
			if err == nil && dirExists(filepath.Join(base, dir)) {
				found = append(found, agent)
			}
		}
		switch len(found) {
		case 1:
			ctx.svc.Agent = found[0]
			ctx.agents = found
			dir, _ := skill.SkillsDir(found[0])
			ctx.agentNote = fmt.Sprintf("sem orotools.yaml: agent detectado por %s (use --agent para escolher outro)", dir)
		case 0:
			ctx.svc.Agent = "opencode"
			ctx.agents = []string{"opencode"}
			ctx.agentNote = "sem orotools.yaml: nenhum diretório de skills encontrado; usando .opencode/skills (use --agent para escolher outro)"
		default:
			if o.pickAgents == nil {
				return skillsContext{}, fmt.Errorf("múltiplos diretórios de skills encontrados (%v); use --agent para escolher", found)
			}
			chosen, err := o.pickAgents(found)
			if err != nil {
				return skillsContext{}, err
			}
			if len(chosen) == 0 {
				return skillsContext{}, fmt.Errorf("nenhum agent selecionado")
			}
			ctx.agents = orderAgents(chosen)
			ctx.svc.Agent = ctx.agents[0]
			ctx.agentNote = fmt.Sprintf("sem orotools.yaml: skills serão instaladas em %s", strings.Join(agentDirs(ctx.agents), ", "))
		}
		return ctx, nil
	}
}

// orderAgents devolve os agents na ordem canônica de AllAgents — o relatório e
// o lock ficam determinísticos independentemente da ordem marcada na seleção.
func orderAgents(agents []string) []string {
	set := make(map[string]bool, len(agents))
	for _, a := range agents {
		set[a] = true
	}
	ordered := make([]string, 0, len(agents))
	for _, a := range skill.AllAgents {
		if set[a] {
			ordered = append(ordered, a)
		}
	}
	return ordered
}

// agentDirs maps agent ids to their skills directories for display.
func agentDirs(agents []string) []string {
	dirs := make([]string, 0, len(agents))
	for _, a := range agents {
		if d, err := skill.SkillsDir(a); err == nil {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// runSkills implements `oro skills`: shows installed skills, migrates legacy
// bundled skills into the lock and interactively installs the selection
// (skills-manager FR-1, FR-11, FR-15, FR-16). Without an orotools.yaml it
// runs in ad-hoc mode: GitHub catalog only, agent detected or chosen.
func runSkills(opts skillsOptions) error {
	out := opts.out
	status := ui.NewStatus(out)

	// A seleção de agents só existe com terminal: sem stdin interativo a
	// ambiguidade permanece o erro acionável de hoje (skills-multi-agent FR-5).
	pickAgents := opts.pickAgents
	if pickAgents == nil && isInteractiveStdin(opts) {
		pickAgents = defaultPickAgents(opts)
	}
	ctx, err := loadSkillsContext(contextOptions{
		manifestPath:     opts.manifestPath,
		manifestExplicit: opts.manifestExplicit,
		agentFlag:        opts.agentFlag,
		interactive:      true,
		pickAgents:       pickAgents,
	})
	if err != nil {
		if errors.Is(err, errPickerCancel) {
			status.Info("cancelado")
			return nil
		}
		return fmt.Errorf("skills: %w", err)
	}
	agents := ctx.agents
	if len(agents) == 0 {
		agents = []string{ctx.svc.Agent}
	}
	svc, lock := ctx.svc, ctx.lock
	if ctx.agentNote != "" {
		status.Info(ctx.agentNote)
	}
	if ctx.adHoc {
		status.Info("sem orotools.yaml: catálogo bundled indisponível (apenas skills_AI); o lock fica em .orotools/ mesmo assim")
	}

	// The wizard is interactive by nature (FR-4); the non-interactive path is
	// `oro skills update`. Checked before any mutation.
	if !isInteractiveStdin(opts) {
		return fmt.Errorf("skills: stdin não interativo; o wizard precisa de um terminal (use `oro skills update` para operação não interativa)")
	}
	svc.CleanOrphans()

	// Bundled catalog only exists with a manifest; errors are fatal packaging
	// problems (FR-7).
	var bundled []skill.Candidate
	if !ctx.adHoc {
		bundled, err = loadBundledCatalog(opts, ctx.m)
		if err != nil {
			return fmt.Errorf("skills: %w", err)
		}
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
	var migration []skill.MigrationResult
	if !ctx.adHoc {
		migration, err = svc.MigrateBundled(lock, bundled)
		if err != nil {
			return fmt.Errorf("skills: %w", err)
		}
	}

	candidates := append(append([]skill.Candidate(nil), bundled...), remote...)
	available := make([]skill.Candidate, 0, len(candidates))
	for _, cand := range candidates {
		// Multi-agent: a skill é ofertada enquanto falta em pelo menos um dos
		// agents de destino; os que já a têm caem em "já instalada" (FR-9).
		if !installedInAllAgents(lock, agents, cand) {
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
			ID:          cand.ID,
			Name:        cand.Name,
			Label:       fmt.Sprintf("%s %s %s — %s", cand.Name, cand.Version.String(), sourceLabel(string(cand.Source), cand.SourceRef), cand.Description),
			Description: cand.Description,
			Version:     cand.Version.String(),
			Source:      string(cand.Source),
			SourceRef:   cand.SourceRef,
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
		// Um par (skill × agent) por instalação, best-effort: a falha em um
		// destino não aborta os demais (FR-7/FR-8).
		for _, agent := range agents {
			svcAgent := svc
			svcAgent.Agent = agent
			res, err := svcAgent.Install(ctx.m, lock, cand)
			switch {
			case err != nil:
				status.Error(fmt.Sprintf("%s: %v", cand.Name, err))
				failures = append(failures, cand.ID+"@"+agent)
			case res.Already:
				status.Info(fmt.Sprintf("%s %s já instalada em %s (conteúdo idêntico)", res.Name, res.Version, res.Target))
				installedCount++
			default:
				status.Success(fmt.Sprintf("%s %s instalada em %s", res.Name, res.Version, res.Target))
				installedCount++
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("skills: %d instalação(ões) falharam", len(failures))
	}
	status.Success(fmt.Sprintf("%d skill(s) instalada(s).", installedCount))
	return nil
}

// installedInAllAgents reports whether the candidate is registered in the lock
// at the target of every destination agent.
func installedInAllAgents(lock *skill.Lock, agents []string, cand skill.Candidate) bool {
	for _, agent := range agents {
		dir, err := skill.SkillsDir(agent)
		if err != nil {
			return false
		}
		if _, ok := lock.LookupTarget(cand.ID, path.Join(dir, cand.Name)); !ok {
			return false
		}
	}
	return true
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

// renderInstalled prints the installed skills from the lock (FR-11). With
// skills on more than one agent the target directory disambiguates the lines
// (skills-multi-agent FR-13).
func renderInstalled(out io.Writer, lock *skill.Lock) {
	if len(lock.Skills) == 0 {
		return
	}
	dirs := make(map[string]bool)
	for _, e := range lock.Skills {
		dirs[path.Dir(e.Target)] = true
	}
	fmt.Fprintln(out, "Skills já instaladas")
	marker := listMarker(out)
	for _, e := range lock.Skills {
		if len(dirs) > 1 {
			fmt.Fprintf(out, "  %c %-28s %-8s %-22s %s\n", marker, e.Name, e.Version, e.Target, sourceLabel(e.Source, e.SourceRef))
			continue
		}
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
	manifestPath     string
	manifestExplicit bool
	agentFlag        string
	force            bool
	dryRun           bool
	out              io.Writer
	errOut           io.Writer

	recipeCatalog skill.CatalogProvider
	loadGitHub    func(context.Context) ([]skill.Candidate, []skill.Warning, func(), error)
}

// runSkillsUpdate implements `oro skills update`: compares every lock entry
// with the catalogs and applies updates best-effort, with a deterministic
// report and an aggregated exit code (skills-manager FR-1, FR-27, FR-28).
// Without an orotools.yaml it updates only GitHub-managed entries.
func runSkillsUpdate(opts skillsUpdateOptions) error {
	out := opts.out
	status := ui.NewStatus(out)

	ctx, err := loadSkillsContext(contextOptions{
		manifestPath:     opts.manifestPath,
		manifestExplicit: opts.manifestExplicit,
		agentFlag:        opts.agentFlag,
		interactive:      false,
	})
	if err != nil {
		return fmt.Errorf("skills update: %w", err)
	}
	svc, lock := ctx.svc, ctx.lock
	if ctx.agentNote != "" {
		status.Info(ctx.agentNote)
	}
	svc.CleanOrphans()

	// Bundled catalog: fatal on packaging problems (FR-7). Ad-hoc mode has no
	// recipe to resolve, so bundled entries in the lock fail per-item.
	bundled := []skill.Candidate(nil)
	if !ctx.adHoc {
		bundled, err = loadBundledCatalog(skillsOptions{recipeCatalog: opts.recipeCatalog}, ctx.m)
		if err != nil {
			return fmt.Errorf("skills update: %w", err)
		}
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

	migration := []skill.MigrationResult(nil)
	if !ctx.adHoc {
		migration, err = svc.MigrateBundled(lock, bundled)
		if err != nil {
			return fmt.Errorf("skills update: %w", err)
		}
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
		// Produção usa a TUI full-screen; huh fica como fallback quando a
		// saída não é um terminal (a TUI precisa de alt-screen).
		if ui.IsTTY(opts.out) {
			sel = tuiSkillSelector{version: activeVersion}
		} else {
			sel = huhSkillSelector{}
		}
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
	if err := newOroForm(huh.NewGroup(field)).Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

// defaultPickAgents resolves the production agent picker: full-screen TUI when
// the output is a terminal, huh wizard otherwise — the same rule as
// selectSkills.
func defaultPickAgents(opts skillsOptions) func(candidates []string) ([]string, error) {
	return func(candidates []string) ([]string, error) {
		choices := make([]agentChoice, 0, len(candidates))
		for _, id := range candidates {
			dir, _ := skill.SkillsDir(id)
			choices = append(choices, agentChoice{id: id, dir: dir})
		}
		var sel agentSelector = huhAgentSelector{}
		if ui.IsTTY(opts.out) {
			sel = tuiAgentSelector{}
		}
		return sel.Select("Selecione os agents de destino", choices)
	}
}

// agentSelector asks for one or more destination agents.
type agentSelector interface {
	Select(title string, choices []agentChoice) ([]string, error)
}

// huhAgentSelector is the agent picker fallback when the output is not a
// terminal; enter without a selection re-prompts (FR-2).
type huhAgentSelector struct{}

func (huhAgentSelector) Select(title string, choices []agentChoice) ([]string, error) {
	huhOpts := make([]huh.Option[string], 0, len(choices))
	for _, c := range choices {
		label := c.id
		if c.dir != "" {
			label = fmt.Sprintf("%s (%s)", c.id, c.dir)
		}
		huhOpts = append(huhOpts, huh.NewOption(label, c.id))
	}
	var selected []string
	field := huh.NewMultiSelect[string]().
		Title(title).
		Options(huhOpts...).
		Value(&selected).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("selecione ao menos um agent")
			}
			return nil
		})
	if err := newOroForm(huh.NewGroup(field)).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, errPickerCancel
		}
		return nil, err
	}
	return selected, nil
}

func sourceLabel(source, sourceRef string) string {
	return source + ":" + sourceRef
}

// dirExists reports whether the directory exists in the filesystem.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
