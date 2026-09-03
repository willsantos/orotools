package agent

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// BundledOptions configures bundled agent installation.
type BundledOptions struct {
	Base     string
	Agent    string
	RecipeFS fs.FS
	Bundled  []string
	Stdout   io.Writer
	Stderr   io.Writer
}

// InstallBundled copies bundled agent markdown files from the recipe FS.
func InstallBundled(o BundledOptions) error {
	if len(o.Bundled) == 0 {
		return nil
	}
	destRoot, err := AgentsDir(o.Agent)
	if err != nil {
		return err
	}
	for _, name := range o.Bundled {
		srcRel := path.Join("agents", name+".md")
		destRel := path.Join(destRoot, name+".md")
		if err := copyFileCreateOnly(o.Base, o.RecipeFS, srcRel, destRel); err != nil {
			return fmt.Errorf("agent %q: %w", name, err)
		}
	}
	return nil
}

func copyFileCreateOnly(base string, srcFS fs.FS, srcRel, destRel string) error {
	destAbs, err := safeJoin(base, destRel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destAbs); err == nil {
		return nil
	}
	data, err := fs.ReadFile(srcFS, srcRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destAbs, data, 0o644)
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
