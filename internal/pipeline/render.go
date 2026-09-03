package pipeline

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

func loadTemplate(recipeFS fs.FS, recipeName, templateRel, providerKey string) (string, error) {
	if recipeFS != nil {
		if b, err := fs.ReadFile(recipeFS, templateRel); err == nil {
			return string(b), nil
		}
	}
	if tmpl, ok := defaultTemplate(providerKey, recipeName); ok {
		return tmpl, nil
	}
	if tmpl, ok := defaultTemplate(providerKey, "generic"); ok {
		return tmpl, nil
	}
	return "", fmt.Errorf("no pipeline template for provider %q and recipe %q", providerKey, recipeName)
}

func renderTemplate(content string, project Project, vars map[string]any) ([]byte, error) {
	tmpl, err := template.New("pipeline").Parse(content)
	if err != nil {
		return nil, fmt.Errorf("parse pipeline template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct {
		Project Project
		Vars    map[string]any
	}{project, vars}); err != nil {
		return nil, fmt.Errorf("execute pipeline template: %w", err)
	}
	return buf.Bytes(), nil
}

func writeCreateOnly(base, destRel string, data []byte) (created bool, err error) {
	dest, err := safeJoin(base, destRel)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(dest); err == nil {
		return false, nil
	}
	if err := writeFile(dest, data); err != nil {
		return false, err
	}
	return true, nil
}

func writeFile(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

func safeJoin(base, dest string) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("destination is empty")
	}
	joined := filepath.Join(base, dest)
	rel, err := filepath.Rel(base, joined)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be made relative to base %q: %w", dest, base, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base %q", dest, base)
	}
	return joined, nil
}

// Render loads, renders and writes a pipeline file using create-only idempotency.
func Render(templateRel, destRel, providerKey string, project Project, o Options) (created bool, err error) {
	content, err := loadTemplate(o.RecipeFS, o.RecipeName, templateRel, providerKey)
	if err != nil {
		return false, err
	}
	out, err := renderTemplate(content, project, o.Vars)
	if err != nil {
		return false, err
	}
	return writeCreateOnly(o.Base, destRel, out)
}
