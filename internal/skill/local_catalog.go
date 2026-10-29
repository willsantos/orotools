package skill

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LocalCatalog reads an explicitly authorized local checkout without executing
// files. SourceRef is a stable, credential-free origin identifier, not a path.
type LocalCatalog struct {
	Root      string
	SourceRef string
	Revision  string
	Limits    SnapshotLimits
}

var _ CatalogProvider = LocalCatalog{}

func LocalID(sourceRef, skillPath string) string {
	return fmt.Sprintf("local:%s/%s", sourceRef, skillPath)
}

func (c LocalCatalog) List(ctx context.Context) ([]Candidate, []Warning, error) {
	if c.SourceRef == "" || strings.ContainsAny(c.SourceRef, "\r\n\x00") {
		return nil, nil, fmt.Errorf("local catalog: sourceRef must be a stable credential-free identifier")
	}
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return nil, nil, fmt.Errorf("local catalog: resolve root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, fmt.Errorf("local catalog: resolve root symlinks: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("local catalog: root is not a directory: %s", root)
	}
	fsys := os.DirFS(root)
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("local catalog %q: %w", c.SourceRef, err)
	}
	limits := c.Limits.withDefaults()
	if len(entries) > limits.MaxEntries {
		return nil, nil, fmt.Errorf("local catalog %q: more than %d entries", c.SourceRef, limits.MaxEntries)
	}
	var candidates []Candidate
	var warnings []Warning
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if err := validateDestinationName(entry.Name()); err != nil {
			warnings = append(warnings, Warning{SkillID: LocalID(c.SourceRef, entry.Name()), Message: err.Error()})
			continue
		}
		skillRoot := path.Clean(entry.Name())
		file, err := fsys.Open(path.Join(skillRoot, "SKILL.md"))
		if err != nil {
			continue
		}
		metadata, parseErr := ParseMetadata(file)
		_ = file.Close()
		if parseErr == nil && metadata.Name != entry.Name() {
			parseErr = fmt.Errorf("frontmatter name %q differs from directory %q", metadata.Name, entry.Name())
		}
		if parseErr == nil {
			parseErr = checkSkillSize(fsys, skillRoot, limits)
		}
		if parseErr != nil {
			warnings = append(warnings, Warning{SkillID: LocalID(c.SourceRef, entry.Name()), Message: parseErr.Error()})
			continue
		}
		candidates = append(candidates, Candidate{
			ID: LocalID(c.SourceRef, entry.Name()), Name: metadata.Name,
			Description: metadata.Description, Version: metadata.Version,
			Source: SourceLocal, SourceRef: c.SourceRef, Path: entry.Name(), Revision: c.Revision,
			Tree: Tree{FS: fsys, Root: skillRoot},
		})
	}
	SortCandidates(candidates)
	if duplicates := DestinationCollisions(candidates); len(duplicates) != 0 {
		return nil, nil, fmt.Errorf("local catalog %q: conflicting destinations: %v", c.SourceRef, duplicates)
	}
	return candidates, warnings, nil
}
