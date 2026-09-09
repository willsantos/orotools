package skill

import (
	"context"
	"fmt"
	"io/fs"
	"path"
)

// RecipeCatalog turns the skills.bundled refs of a single recipe into
// versioned candidates (skills-manager FR-5). Only refs declared by the
// manifest recipe are listed — never assets of other recipes.
type RecipeCatalog struct {
	RecipeName string
	FS         fs.FS
	Refs       []string
}

// Compile-time check that RecipeCatalog satisfies the catalog contract.
var _ CatalogProvider = RecipeCatalog{}

// List resolves every bundled ref. A missing ref or invalid metadata is a
// packaging error and aborts the catalog (bundled content ships with the
// binary, so an inconsistency cannot be skipped silently — FR-7).
func (c RecipeCatalog) List(ctx context.Context) ([]Candidate, []Warning, error) {
	cands := make([]Candidate, 0, len(c.Refs))
	for _, ref := range c.Refs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		root := path.Join("skills", ref)
		f, err := c.FS.Open(path.Join(root, "SKILL.md"))
		if err != nil {
			return nil, nil, fmt.Errorf("catalog recipe %q: skill %q: %w", c.RecipeName, ref, err)
		}
		md, err := ParseMetadata(f)
		f.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("catalog recipe %q: skill %q: %w", c.RecipeName, ref, err)
		}
		cands = append(cands, Candidate{
			ID:          RecipeID(c.RecipeName, ref),
			Name:        md.Name,
			Description: md.Description,
			Version:     md.Version,
			Source:      SourceRecipe,
			SourceRef:   c.RecipeName,
			Path:        ref,
			Tree:        Tree{FS: c.FS, Root: root},
		})
	}
	SortCandidates(cands)
	if dups := DestinationCollisions(cands); len(dups) > 0 {
		return nil, nil, fmt.Errorf("catalog recipe %q: conflicting destinations: %v", c.RecipeName, dups)
	}
	return cands, nil, nil
}
