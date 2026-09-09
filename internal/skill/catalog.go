package skill

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	"github.com/Masterminds/semver/v3"
)

// CatalogProvider lists installable candidates (skills-manager design).
// Non-fatal observations come back as warnings; a returned error aborts the
// whole catalog.
type CatalogProvider interface {
	List(ctx context.Context) ([]Candidate, []Warning, error)
}

// Warning is a non-fatal catalog observation — typically a remote entry
// omitted for being invalid (skills-manager FR-7).
type Warning struct {
	SkillID string
	Message string
}

// SourceKind identifies where a catalog candidate comes from.
type SourceKind string

const (
	// SourceRecipe marks skills bundled in an official recipe.
	SourceRecipe SourceKind = "recipe"
	// SourceGitHub marks skills from the public GitHub catalog.
	SourceGitHub SourceKind = "github"
)

// GitHubCatalogRef is the fixed public skills catalog (skills-manager FR-6).
const GitHubCatalogRef = "github:willsantos/skills_AI"

// Tree locates a candidate's file tree inside a filesystem. Embedded recipe
// filesystems and extracted snapshot directories (os.DirFS) both satisfy
// fs.FS, so the installer copies from either uniformly.
type Tree struct {
	FS   fs.FS
	Root string // slash-separated path of the skill directory inside FS
}

// Candidate is one installable skill entry of a catalog.
type Candidate struct {
	ID          string // stable identity: source + path (FR-9)
	Name        string // from metadata; basename of the destination
	Description string
	Version     semver.Version
	Source      SourceKind
	SourceRef   string // recipe name or GitHub repo slug
	Path        string // full path of the skill inside the source
	Revision    string // commit SHA for GitHub skills; empty for bundled
	Tree        Tree
}

// RecipeID returns the stable identity of a bundled skill (FR-9 conventions):
// recipe:<recipe>/<path-da-skill>.
func RecipeID(recipeName, ref string) string {
	return fmt.Sprintf("recipe:%s/%s", recipeName, ref)
}

// GitHubID returns the stable identity of a catalog skill:
// github:willsantos/skills_AI/<diretório>.
func GitHubID(dir string) string {
	return fmt.Sprintf("%s/%s", GitHubCatalogRef, dir)
}

// SortCandidates orders candidates deterministically (skills-manager FR-9):
// bundled before GitHub, then by name, then by ID as tiebreak.
func SortCandidates(cands []Candidate) {
	sort.Slice(cands, func(i, j int) bool {
		pi, pj := sourceRank(cands[i].Source), sourceRank(cands[j].Source)
		if pi != pj {
			return pi < pj
		}
		if cands[i].Name != cands[j].Name {
			return cands[i].Name < cands[j].Name
		}
		return cands[i].ID < cands[j].ID
	})
}

func sourceRank(s SourceKind) int {
	if s == SourceGitHub {
		return 1
	}
	return 0
}

// DestinationCollisions returns the sorted names claimed by more than one
// candidate. Two identities resolving to the same destination directory are an
// explicit conflict, never a silent overwrite.
func DestinationCollisions(cands []Candidate) []string {
	count := make(map[string]int, len(cands))
	for _, c := range cands {
		count[c.Name]++
	}
	var dups []string
	for name, n := range count {
		if n > 1 {
			dups = append(dups, name)
		}
	}
	sort.Strings(dups)
	return dups
}
