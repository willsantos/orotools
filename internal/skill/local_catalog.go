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
//
// Two layouts are discovered in the same checkout: the public distribution
// layout (`<skill>/SKILL.md` at the checkout root) and the authoring layout
// (`skills/<skill>/SKILL.md`). The same skill name in both layouts is a
// conflict and fails closed — it cannot be decided which one is authoritative.
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

// authoringSkillsDir is the authoring-repo subdirectory scanned in addition to
// the checkout root. It must be a real directory, never a symlink.
const authoringSkillsDir = "skills"

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
	dirs := []string{"."}
	if skillsInfo, err := os.Lstat(filepath.Join(root, authoringSkillsDir)); err == nil && skillsInfo.Mode().IsDir() {
		dirs = append(dirs, authoringSkillsDir)
	}
	limits := c.Limits.withDefaults()
	var candidates []Candidate
	var warnings []Warning
	for _, dir := range dirs {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return nil, nil, fmt.Errorf("local catalog %q: %w", c.SourceRef, err)
		}
		if len(entries) > limits.MaxEntries {
			return nil, nil, fmt.Errorf("local catalog %q: more than %d entries", c.SourceRef, limits.MaxEntries)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if dir == authoringSkillsDir && entry.Name() == authoringSkillsDir {
				continue
			}
			if err := validateDestinationName(entry.Name()); err != nil {
				warnings = append(warnings, Warning{SkillID: LocalID(c.SourceRef, entry.Name()), Message: err.Error()})
				continue
			}
			candidate, warning, ok := c.inspectEntry(fsys, dir, entry.Name(), limits)
			if warning != nil {
				warnings = append(warnings, *warning)
			}
			if ok {
				candidates = append(candidates, candidate)
			}
		}
	}
	SortCandidates(candidates)
	if duplicates := DestinationCollisions(candidates); len(duplicates) != 0 {
		return nil, nil, fmt.Errorf("local catalog %q: conflicting destinations: %v", c.SourceRef, duplicates)
	}
	return candidates, warnings, nil
}

// inspectEntry validates one candidate directory. ok reports whether the
// entry is a valid skill; a non-skill entry (no SKILL.md) is silently skipped.
func (c LocalCatalog) inspectEntry(fsys fs.FS, dir, name string, limits SnapshotLimits) (Candidate, *Warning, bool) {
	skillRoot := path.Clean(path.Join(dir, name))
	file, err := fsys.Open(path.Join(skillRoot, "SKILL.md"))
	if err != nil {
		return Candidate{}, nil, false
	}
	metadata, parseErr := ParseMetadata(file)
	_ = file.Close()
	if parseErr == nil && metadata.Name != name {
		parseErr = fmt.Errorf("frontmatter name %q differs from directory %q", metadata.Name, name)
	}
	if parseErr == nil {
		parseErr = checkSkillSize(fsys, skillRoot, limits)
	}
	if parseErr != nil {
		return Candidate{}, &Warning{SkillID: LocalID(c.SourceRef, name), Message: parseErr.Error()}, false
	}
	return Candidate{
		ID: LocalID(c.SourceRef, name), Name: metadata.Name,
		Description: metadata.Description, Version: metadata.Version,
		Source: SourceLocal, SourceRef: c.SourceRef, Path: skillRoot, Revision: c.Revision,
		Tree: Tree{FS: fsys, Root: skillRoot},
	}, nil, true
}
