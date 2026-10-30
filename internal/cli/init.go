package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/onboarding"
	"oroborus.dev/orotools/internal/ui"
)

// initOptions carries parsed flags and streams for `oro init`.
type initOptions struct {
	yes             bool
	onboarding      bool
	dryRun          bool
	branches        string
	branchesChanged bool
	out             io.Writer
	cwd             string // directory to analyse; defaults to current working dir
}

// detection holds what init could infer about the existing project.
type detection struct {
	ProjectName    string
	Recipe         string
	PackageManager string
	HasGit         bool
	Markers        []string
}

// runInit detects stack/git/package-manager in an existing project, suggests a
// recipe and writes orotools.yaml (spec section 55). With the explicit
// --onboarding opt-in it additionally plans and, with --yes, applies Git
// onboarding through the onboarding sealed plan. Onboarding problems are
// reported and block only that action: init never fails as a whole because of
// them, and nothing is written without --yes (or with --dry-run).
func runInit(opts initOptions) error {
	out := writerOrStdout(opts.out)
	status := ui.NewStatus(out)
	if opts.yes && opts.dryRun {
		return fmt.Errorf("--yes e --dry-run não podem ser combinados")
	}
	if opts.branchesChanged && !opts.onboarding {
		return fmt.Errorf("--protect-branches requer --onboarding")
	}
	if err := validateProtectionFlag(opts.branches, opts.branchesChanged); err != nil {
		return err
	}
	protection, err := onboardingOptions(opts.branches)
	if err != nil {
		return err
	}
	cwd := opts.cwd
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	d := detect(cwd)
	status.Title("Orotools init — detecção")
	fmt.Fprintln(out)
	for _, m := range d.Markers {
		status.Success(m)
	}
	if d.HasGit {
		status.Success("git")
	} else if opts.onboarding {
		status.Warn("diretório não é um repositório Git; onboarding só se aplica a repositórios Git")
	}
	if d.PackageManager != "" {
		status.Success(fmt.Sprintf("gerenciador de pacotes: %s", d.PackageManager))
	}
	if d.Recipe == "" {
		status.Warn("não foi possível sugerir uma recipe (nenhum marcador conhecido encontrado)")
	} else {
		status.Success(fmt.Sprintf("recipe sugerida: %s", d.Recipe))
	}
	fmt.Fprintln(out)

	if opts.dryRun {
		status.Info(fmt.Sprintf("dry-run: orotools.yaml seria escrito em %s", filepath.Join(cwd, manifest.Filename)))
		if opts.onboarding {
			if err := previewInitOnboarding(out, cwd, protection); err != nil {
				status.Warn(fmt.Sprintf("onboarding: %v", err))
			}
		}
		return nil
	}

	if !opts.yes {
		// Interactive confirmation would use huh here; for v0.1 we require --yes
		// to write, otherwise we just report and let the user re-run with --yes.
		status.Info("rode novamente com --yes para escrever o orotools.yaml")
		if opts.onboarding {
			if err := previewInitOnboarding(out, cwd, protection); err != nil {
				status.Warn(fmt.Sprintf("onboarding: %v", err))
			}
			status.Info("onboarding é opt-in: com --yes o preview é aplicado após o manifest")
		}
		return nil
	}

	m := &manifest.Manifest{
		Version: 1,
		Project: manifest.ProjectRef{Name: d.ProjectName},
		Recipe:  manifest.RecipeRef{Name: d.Recipe},
	}
	if d.PackageManager != "" {
		m.Variables = map[string]any{"package_manager": d.PackageManager}
	}
	path := filepath.Join(cwd, manifest.Filename)
	if err := manifest.Write(path, m); err != nil {
		return fmt.Errorf("escrever manifest: %w", err)
	}
	status.Success(fmt.Sprintf("escrito: %s", path))

	if opts.onboarding {
		applyInitOnboarding(out, status, cwd, protection)
	}
	return nil
}

// previewInitOnboarding shows the read-only onboarding plan for dir. The error
// return only signals that no preview could be produced (callers downgrade it
// to a warning); conflicts are part of the preview itself.
func previewInitOnboarding(out io.Writer, dir string, protection onboarding.Options) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	plan, err := onboarding.PlanApply(abs, protection)
	if err != nil {
		return err
	}
	printOnboardingPreview(out, plan, protection)
	return nil
}

// applyInitOnboarding applies onboarding after the manifest write. Every
// failure mode — inspection error, conflict, apply refusal — is reported and
// leaves init successful; the adoption simply does not happen.
func applyInitOnboarding(out io.Writer, status *ui.Status, dir string, protection onboarding.Options) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		status.Warn(fmt.Sprintf("onboarding: %v", err))
		return
	}
	plan, err := onboarding.PlanApply(abs, protection)
	if err != nil {
		status.Warn(fmt.Sprintf("onboarding não aplicado: %v", err))
		return
	}
	fmt.Fprintln(out)
	printOnboardingPreview(out, plan, protection)
	if len(plan.Conflicts) > 0 {
		status.Warn(fmt.Sprintf("onboarding não aplicado: %d conflito(s); configuração existente preservada — resolva e use `oro onboarding apply`", len(plan.Conflicts)))
		return
	}
	if err := onboarding.Apply(plan); err != nil {
		status.Warn(fmt.Sprintf("onboarding não aplicado: %v", err))
		return
	}
	status.Success("onboarding aplicado (opt-in): bloco gerenciado de .gitignore" + protectionSummary(protection))
}

func protectionSummary(protection onboarding.Options) string {
	if !protection.EnableProtection {
		return ""
	}
	return fmt.Sprintf(" e hooks de proteção (%s)", strings.Join(protection.ProtectedBranches, ", "))
}

// detect inspects dir and returns what it could infer.
func detect(dir string) detection {
	d := detection{ProjectName: filepath.Base(dir)}

	if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
		d.HasGit = true
	}
	d.PackageManager = detectPackageManager(dir)

	if _, err := os.Stat(filepath.Join(dir, "Gemfile")); err == nil {
		d.Markers = append(d.Markers, "Ruby on Rails")
		d.Recipe = "rails"
	}
	if has, _ := hasFileRecursive(dir, ".csproj"); has {
		// Distinguish dotnet vs dotnet-next by presence of a web app.
		if _, err := os.Stat(filepath.Join(dir, "apps", "web", "package.json")); err == nil {
			d.Markers = append(d.Markers, ".NET + Next.js (monorepo)")
			d.Recipe = "dotnet-next"
		} else {
			d.Markers = append(d.Markers, ".NET")
			d.Recipe = "dotnet"
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		// Distinguish fastify-next from generic node by monorepo layout.
		apiPkg := filepath.Join(dir, "apps", "api", "package.json")
		webPkg := filepath.Join(dir, "apps", "web", "package.json")
		_, apiErr := os.Stat(apiPkg)
		_, webErr := os.Stat(webPkg)
		if apiErr == nil && webErr == nil {
			// Heuristic: only flag fastify-next if no csproj already claimed dotnet-next.
			if !contains(d.Markers, ".NET + Next.js (monorepo)") {
				d.Markers = append(d.Markers, "Node monorepo (apps/api + apps/web)")
				if d.Recipe == "" {
					d.Recipe = "fastify-next"
				}
			}
		} else if d.Recipe == "" {
			d.Markers = append(d.Markers, "Node.js")
		}
	}

	return d
}

func detectPackageManager(dir string) string {
	cases := []struct {
		marker, pm string
	}{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"package-lock.json", "npm"},
		{"bun.lockb", "bun"},
	}
	for _, c := range cases {
		if _, err := os.Stat(filepath.Join(dir, c.marker)); err == nil {
			return c.pm
		}
	}
	return ""
}

// hasFileRecursive reports whether any file with the given suffix exists in dir
// or any of its subdirectories (used for *.csproj detection).
func hasFileRecursive(dir, suffix string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.HasSuffix(path, suffix) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}
