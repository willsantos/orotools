package skill

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GitHubRepoSlug is the fixed public catalog repository (skills-manager FR-6).
const GitHubRepoSlug = "willsantos/skills_AI"

// SnapshotLimits bound what the catalog accepts from the remote repository
// (skills-manager FR-32).
type SnapshotLimits struct {
	MaxEntries       int   // catalog entries (root skill directories)
	MaxFilesPerSkill int   // regular files per skill
	MaxSkillBytes    int64 // total bytes per skill
	MaxFileBytes     int64 // single file during extraction
	MaxTotalBytes    int64 // whole snapshot during extraction
}

// DefaultSnapshotLimits are the limits mandated by skills-manager FR-32.
var DefaultSnapshotLimits = SnapshotLimits{
	MaxEntries:       200,
	MaxFilesPerSkill: 500,
	MaxSkillBytes:    20 << 20,
	MaxFileBytes:     20 << 20,
	MaxTotalBytes:    50 << 20,
}

func (l SnapshotLimits) withDefaults() SnapshotLimits {
	if l == (SnapshotLimits{}) {
		return DefaultSnapshotLimits
	}
	return l
}

// GitHubSnapshot is an extracted, pinned copy of the catalog repository at a
// commit SHA. Cleanup removes the extraction directory and is safe to call
// more than once. The caller that fetched the snapshot owns its lifecycle.
type GitHubSnapshot struct {
	Revision string
	Dir      string // local extraction directory backing Tree
	Tree     Tree   // rooted at the repository root
	Cleanup  func()
}

// GitHubClient resolves the catalog repository to a commit and fetches a
// pinned snapshot (skills-manager design: contract testable, no real network
// in tests — NFR-2).
type GitHubClient interface {
	Resolve(ctx context.Context) (revision string, err error)
	Snapshot(ctx context.Context, revision string) (GitHubSnapshot, error)
}

// GitHubCatalog discovers skills in a pinned snapshot of the public catalog
// repository. The snapshot is resolved once and shared by the whole run
// (skills-manager FR-33: every file belongs to the same commit).
type GitHubCatalog struct {
	Snapshot GitHubSnapshot
	Limits   SnapshotLimits
}

var _ CatalogProvider = GitHubCatalog{}

// List inspects every root directory of the snapshot. A directory without
// SKILL.md is not a skill and is ignored; a skill with invalid metadata or
// breaching per-skill limits is omitted with a warning (FR-7, FR-32).
// Catalog-level breaches (too many entries, colliding destinations) are
// errors.
func (c GitHubCatalog) List(ctx context.Context) ([]Candidate, []Warning, error) {
	limits := c.Limits.withDefaults()
	tree := c.Snapshot.Tree
	entries, err := fs.ReadDir(tree.FS, tree.Root)
	if err != nil {
		return nil, nil, fmt.Errorf("catalog %s: %w", GitHubCatalogRef, err)
	}
	cands := make([]Candidate, 0, len(entries))
	var warns []Warning
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !entry.IsDir() {
			continue
		}
		if len(cands) >= limits.MaxEntries {
			return nil, nil, fmt.Errorf("catalog %s: more than %d entries (FR-32)", GitHubCatalogRef, limits.MaxEntries)
		}
		dir := path.Join(tree.Root, entry.Name())
		mdFile, err := tree.FS.Open(path.Join(dir, "SKILL.md"))
		if err != nil {
			continue // not a skill
		}
		md, err := ParseMetadata(mdFile)
		mdFile.Close()
		if err != nil {
			warns = append(warns, Warning{
				SkillID: GitHubID(entry.Name()),
				Message: err.Error(),
			})
			continue
		}
		if werr := checkSkillSize(tree.FS, dir, limits); werr != nil {
			warns = append(warns, Warning{
				SkillID: GitHubID(entry.Name()),
				Message: werr.Error(),
			})
			continue
		}
		cands = append(cands, Candidate{
			ID:          GitHubID(entry.Name()),
			Name:        md.Name,
			Description: md.Description,
			Version:     md.Version,
			Source:      SourceGitHub,
			SourceRef:   GitHubRepoSlug,
			Path:        entry.Name(),
			Revision:    c.Snapshot.Revision,
			Tree:        Tree{FS: tree.FS, Root: dir},
		})
	}
	SortCandidates(cands)
	if dups := DestinationCollisions(cands); len(dups) > 0 {
		return nil, nil, fmt.Errorf("catalog %s: conflicting destinations: %v", GitHubCatalogRef, dups)
	}
	return cands, warns, nil
}

// checkSkillSize enforces the per-skill limits over regular files (FR-32).
func checkSkillSize(fsys fs.FS, root string, limits SnapshotLimits) error {
	var files int
	var total int64
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("file %q is not a regular file", p)
		}
		files++
		total += info.Size()
		return nil
	})
	if err != nil {
		return err
	}
	if files > limits.MaxFilesPerSkill {
		return fmt.Errorf("skill has %d files, limit is %d", files, limits.MaxFilesPerSkill)
	}
	if total > limits.MaxSkillBytes {
		return fmt.Errorf("skill has %d bytes, limit is %d", total, limits.MaxSkillBytes)
	}
	return nil
}

// HTTPGitHubClient implements GitHubClient over the GitHub REST API and
// tarball endpoint (skills-manager FR-31: HTTPS; GITHUB_TOKEN optional and
// never logged).
type HTTPGitHubClient struct {
	BaseURL string         // default https://api.github.com; overridable in tests
	Repo    string         // default GitHubRepoSlug
	Token   string         // optional bearer token; never logged or persisted
	Limits  SnapshotLimits // extraction caps; zero value means the defaults
	HTTP    *http.Client   // default client with 60s timeout
}

// Compile-time check that HTTPGitHubClient satisfies the client contract.
var _ GitHubClient = HTTPGitHubClient{}

func (c HTTPGitHubClient) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.github.com"
}

func (c HTTPGitHubClient) repo() string {
	if c.Repo != "" {
		return c.Repo
	}
	return GitHubRepoSlug
}

func (c HTTPGitHubClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c HTTPGitHubClient) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "orotools")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub respondeu %s (limite de requisições; GITHUB_TOKEN pode aumentar o limite)", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub respondeu %s para %s", resp.Status, strings.TrimPrefix(url, c.baseURL()))
	}
	return resp, nil
}

type githubRepoInfo struct {
	DefaultBranch string `json:"default_branch"`
}

type githubCommit struct {
	SHA string `json:"sha"`
}

// Resolve returns the commit SHA of the repository default branch (one call
// pins the whole run — FR-8, FR-33).
func (c HTTPGitHubClient) Resolve(ctx context.Context) (string, error) {
	resp, err := c.get(ctx, c.baseURL()+"/repos/"+c.repo())
	if err != nil {
		return "", err
	}
	var info githubRepoInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		resp.Body.Close()
		return "", fmt.Errorf("decodificar infos do repositório: %w", err)
	}
	resp.Body.Close()
	if info.DefaultBranch == "" {
		return "", fmt.Errorf("repositório %s sem branch default", c.repo())
	}
	resp, err = c.get(ctx, c.baseURL()+"/repos/"+c.repo()+"/commits/"+urlPathEscape(info.DefaultBranch))
	if err != nil {
		return "", err
	}
	var commit githubCommit
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		resp.Body.Close()
		return "", fmt.Errorf("decodificar commit da branch: %w", err)
	}
	resp.Body.Close()
	if commit.SHA == "" {
		return "", fmt.Errorf("branch %q sem commit", info.DefaultBranch)
	}
	return commit.SHA, nil
}

// Snapshot downloads and extracts the repository tarball at the given commit
// into a temporary directory (never inside the project — design). Symlinks,
// hardlinks and special file types are rejected, and size limits are enforced
// during extraction.
func (c HTTPGitHubClient) Snapshot(ctx context.Context, revision string) (GitHubSnapshot, error) {
	limits := c.Limits.withDefaults()
	resp, err := c.get(ctx, c.baseURL()+"/repos/"+c.repo()+"/tarball/"+urlPathEscape(revision))
	if err != nil {
		return GitHubSnapshot{}, err
	}
	defer resp.Body.Close()

	dir, err := os.MkdirTemp("", "oro-skills-")
	if err != nil {
		return GitHubSnapshot{}, fmt.Errorf("criar diretório de snapshot: %w", err)
	}
	cleanup := sync.OnceFunc(func() { os.RemoveAll(dir) })
	if err := extractTarball(resp.Body, dir, limits); err != nil {
		cleanup()
		return GitHubSnapshot{}, fmt.Errorf("extrair snapshot %s: %w", shortSHA(revision), err)
	}
	return GitHubSnapshot{
		Revision: revision,
		Dir:      dir,
		Tree:     Tree{FS: os.DirFS(dir), Root: "."},
		Cleanup:  cleanup,
	}, nil
}

// extractTarball unpacks a (optionally gzipped) tar stream into dest, stripping
// the single top-level directory GitHub tarballs carry.
func extractTarball(r io.Reader, dest string, limits SnapshotLimits) error {
	buffered := bufio.NewReader(r)
	if magic, err := buffered.Peek(2); err == nil && bytes.Equal(magic, []byte{0x1f, 0x8b}) {
		gz, err := gzip.NewReader(buffered)
		if err != nil {
			return err
		}
		defer gz.Close()
		buffered = bufio.NewReader(gz)
	}
	tr := tar.NewReader(buffered)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeReg:
		case tar.TypeDir:
			continue
		default:
			return fmt.Errorf("entrada %q não é arquivo regular (tipo %q)", hdr.Name, string(hdr.Typeflag))
		}
		rel, err := stripTopDir(hdr.Name)
		if err != nil {
			return err
		}
		if rel == "" {
			continue
		}
		if err := checkRelPath(rel); err != nil {
			return err
		}
		if hdr.Size > limits.MaxFileBytes {
			return fmt.Errorf("arquivo %q (%d bytes) excede o limite por arquivo (%d bytes)", rel, hdr.Size, limits.MaxFileBytes)
		}
		total += hdr.Size
		if total > limits.MaxTotalBytes {
			return fmt.Errorf("snapshot excede o limite total (%d bytes)", limits.MaxTotalBytes)
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
}

// stripTopDir removes the single leading path component GitHub tarballs use
// (e.g. "owner-repo-sha/").
func stripTopDir(name string) (string, error) {
	name = strings.TrimPrefix(name, "./")
	first, rest, found := strings.Cut(name, "/")
	if !found {
		return "", fmt.Errorf("tarball inesperado: entrada %q fora do diretório raiz", name)
	}
	if first == "" || first == "." || first == ".." {
		return "", fmt.Errorf("tarball inesperado: raiz %q", first)
	}
	return rest, nil
}

// checkRelPath rejects traversal and absolute paths in archive entries.
func checkRelPath(rel string) error {
	clean := path.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("caminho suspeito no snapshot: %q", rel)
	}
	return nil
}

func urlPathEscape(s string) string {
	return url.PathEscape(s)
}

// shortSHA keeps error messages short for full-SHA revisions.
func shortSHA(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
