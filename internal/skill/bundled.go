package skill

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// BundledOptions configures bundled skill installation.
type BundledOptions struct {
	Base     string
	Agent    string
	RecipeFS fs.FS
	Bundled  []string
	Stdout   io.Writer
	Stderr   io.Writer
}

// InstallBundled copies bundled skills from the recipe FS into the agent target.
func InstallBundled(o BundledOptions) error {
	if len(o.Bundled) == 0 {
		return nil
	}
	destRoot, err := SkillsDir(o.Agent)
	if err != nil {
		return err
	}
	for _, ref := range o.Bundled {
		srcRel := path.Join("skills", ref)
		destRel := path.Join(destRoot, path.Base(ref))
		if err := copyTreeCreateOnly(o.Base, o.RecipeFS, srcRel, destRel); err != nil {
			return fmt.Errorf("skill %q: %w", ref, err)
		}
	}
	return nil
}

func copyTreeCreateOnly(base string, srcFS fs.FS, srcRel, destRel string) error {
	destAbs, err := safeJoin(base, destRel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destAbs); err == nil {
		return nil
	}
	return fs.WalkDir(srcFS, srcRel, func(walkPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRel, walkPath)
		if err != nil {
			return err
		}
		targetRel := filepath.ToSlash(filepath.Join(destRel, rel))
		targetAbs, err := safeJoin(base, targetRel)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(targetAbs, 0o755)
		}
		data, err := fs.ReadFile(srcFS, walkPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetAbs, data, 0o644)
	})
}

func safeJoin(base, dest string) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("destination is empty")
	}
	joined := filepath.Join(base, filepath.FromSlash(dest))
	rel, err := filepath.Rel(base, joined)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be made relative to base %q: %w", dest, base, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base %q", dest, base)
	}
	return joined, nil
}
