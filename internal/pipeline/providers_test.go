package pipeline_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/pipeline"
	"oroborus.dev/orotools/internal/pipeline/providers"
	"oroborus.dev/orotools/internal/variables"
)

func TestDefaultProvider(t *testing.T) {
	for _, tc := range []struct{ repo, want string }{
		{"github", "github-actions"},
		{"azure-devops", "azure-pipelines"},
		{"local", "none"},
		{"", "none"},
	} {
		if got := pipeline.DefaultProvider(tc.repo); got != tc.want {
			t.Errorf("DefaultProvider(%q) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

func TestProvidersGenerateFiles(t *testing.T) {
	project := variables.Project{Name: "demo", Slug: "demo", Path: "demo"}
	for _, tc := range []struct {
		name    string
		destRel string
	}{
		{"github-actions", ".github/workflows/ci.yml"},
		{"azure-pipelines", "azure-pipelines.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p, err := providers.Get(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Generate(context.Background(), project, pipeline.Options{
				Base:       dir,
				RecipeName: "test-recipe",
				Vars:       map[string]any{"greeting": "hello"},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, tc.destRel)); err != nil {
				t.Fatalf("expected %s: %v", tc.destRel, err)
			}
		})
	}
}

func TestRenderUsesRecipeTemplate(t *testing.T) {
	dir := t.TempDir()
	recipeFS := fstest.MapFS{
		"pipelines/github-actions.yml.tmpl": &fstest.MapFile{
			Data: []byte("name: custom\nproject: {{.Project.Name}}\n"),
		},
	}
	created, err := pipeline.Render(
		"pipelines/github-actions.yml.tmpl",
		".github/workflows/ci.yml",
		"github-actions",
		variables.Project{Name: "acme"},
		pipeline.Options{Base: dir, RecipeName: "test-recipe", RecipeFS: recipeFS},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected file to be created")
	}
	b, err := os.ReadFile(filepath.Join(dir, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "name: custom\nproject: acme\n" {
		t.Errorf("rendered = %q", b)
	}
}

func TestRenderCreateOnlySkipsExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "azure-pipelines.yml")
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := pipeline.Render(
		"pipelines/azure-pipelines.yml.tmpl",
		"azure-pipelines.yml",
		"azure-pipelines",
		variables.Project{Name: "demo"},
		pipeline.Options{Base: dir, RecipeName: "test-recipe", RecipeFS: fs.FS(fstest.MapFS{})},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected existing file to be skipped")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "existing" {
		t.Errorf("file was modified: %q", b)
	}
}

func TestNoneProvider(t *testing.T) {
	dir := t.TempDir()
	p, err := providers.Get("none")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Generate(context.Background(), variables.Project{Name: "demo"}, pipeline.Options{Base: dir}); err != nil {
		t.Fatal(err)
	}
}
