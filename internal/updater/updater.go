package updater

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/creativeprojects/go-selfupdate"
)

const (
	// Owner and Repo compose the public update channel.
	Owner = "willsantos"
	Repo  = "orotools"
	// ReleasesURL is the manual fallback shown when the channel is unreachable.
	ReleasesURL = "https://github.com/willsantos/orotools/releases"
)

// Errors reported by the updater; the CLI maps them to user-facing messages.
var (
	// ErrNoRelease means the channel answered but has no usable release.
	ErrNoRelease = errors.New("nenhuma release encontrada em " + ReleasesURL)
	// ErrVersionNotFound means the requested version has no asset for this OS/arch.
	ErrVersionNotFound = errors.New("versão não encontrada")
	// ErrUnknownCurrentVersion means the local version is not semver (build
	// local sem ldflags), então não há como comparar.
	ErrUnknownCurrentVersion = errors.New("versão local desconhecida")
	// ErrNotWritable means the binary directory denies writes (package-manager
	// install); the update must go through the package manager instead.
	ErrNotWritable = errors.New("sem permissão de escrita no diretório do binário")
)

// Release is the subset of release metadata the CLI reports.
type Release struct {
	Version string // "0.5.0" (semver, sem prefixo v)
	URL     string // página da release no canal
}

// Options configures the updater. Zero value targets the public GitHub channel.
type Options struct {
	// BaseURL overrides the GitHub API base URL ("https://host/api/v3/").
	// Usado em testes (httptest) e para trocar de canal sem mudar a CLI.
	BaseURL string
	// OS/Arch force the target platform; default is runtime values.
	OS, Arch string
}

// Updater checks and applies releases from the configured channel.
type Updater struct {
	up   *selfupdate.Updater
	slug selfupdate.RepositorySlug
}

// New builds an Updater for the public channel (or Options.BaseURL).
func New(opts Options) (*Updater, error) {
	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{
		EnterpriseBaseURL: opts.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("configurando o canal de atualização: %w", err)
	}
	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
		// checksums.txt é gerado pelo GoReleaser no formato "<sha256>  <asset>",
		// exatamente o formato que o ChecksumValidator espera. A validação
		// acontece antes de qualquer substituição do binário (DIST-10).
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
		OS:        opts.OS,
		Arch:      opts.Arch,
	})
	if err != nil {
		return nil, fmt.Errorf("criando o updater: %w", err)
	}
	return &Updater{up: up, slug: selfupdate.NewRepositorySlug(Owner, Repo)}, nil
}

// Latest returns the newest release of the channel.
func (u *Updater) Latest(ctx context.Context) (*Release, error) {
	rel, found, err := u.up.DetectLatest(ctx, u.slug)
	if err != nil {
		return nil, fmt.Errorf("consultando a última release: %w", err)
	}
	if !found {
		return nil, ErrNoRelease
	}
	return fromSelfUpdate(rel), nil
}

// Version returns the release for a specific version ("v0.5.0" ou "0.5.0").
func (u *Updater) Version(ctx context.Context, version string) (*Release, error) {
	rel, found, err := u.up.DetectVersion(ctx, u.slug, toTag(version))
	if err != nil {
		return nil, fmt.Errorf("consultando a release %s: %w", version, err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrVersionNotFound, version)
	}
	return fromSelfUpdate(rel), nil
}

// IsNewer reports whether releaseVersion is strictly greater than currentVersion.
// currentVersion not parseable (ex.: "dev") returns ErrUnknownCurrentVersion.
func IsNewer(releaseVersion, currentVersion string) (bool, error) {
	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return false, fmt.Errorf("%w: %q", ErrUnknownCurrentVersion, currentVersion)
	}
	release, err := semver.NewVersion(releaseVersion)
	if err != nil {
		return false, fmt.Errorf("versão da release inválida %q: %w", releaseVersion, err)
	}
	return release.GreaterThan(current), nil
}

// Apply downloads the release asset for the running platform, validates the
// sha256 against the release checksums.txt and replaces the binary at
// exePath. The download and validation happen before anything is touched: on
// checksum failure the current binary stays intact. The replacement is an
// atomic rename (old binary kept as .old on Windows, where removal of a
// running binary is not possible).
func (u *Updater) Apply(ctx context.Context, rel *Release, exePath string) error {
	abs, err := filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolvendo o caminho do binário: %w", err)
	}
	// Instalações via symlink apontam para o binário real; sem a resolução,
	// o rename substituiria o link em vez do alvo.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if err := checkWritable(filepath.Dir(abs)); err != nil {
		return err
	}
	target, found, err := u.up.DetectVersion(ctx, u.slug, toTag(rel.Version))
	if err != nil {
		return fmt.Errorf("resolvendo a release %s: %w", rel.Version, err)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrVersionNotFound, rel.Version)
	}
	// UpdateTo baixa em memória, valida contra checksums.txt e só então
	// grava .oro.new no diretório do binário e renomeia (DIST-10).
	if err := u.up.UpdateTo(ctx, target, abs); err != nil {
		return fmt.Errorf("atualizando o binário: %w", err)
	}
	return nil
}

// checkWritable probes the directory before downloading anything, so a
// package-manager install (dpkg/rpm) fails fast with a clear cause (DIST-11).
func checkWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".oro-upgrade-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return ErrNotWritable
		}
		return fmt.Errorf("testando escrita em %s: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

func fromSelfUpdate(rel *selfupdate.Release) *Release {
	return &Release{Version: rel.Version(), URL: rel.URL}
}

// toTag normaliza "0.5.0"/"v0.5.0" para a forma de tag usada no canal
// ("v0.5.0"), que é o que o DetectVersion compara com o tag_name.
func toTag(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}
