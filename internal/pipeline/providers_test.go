package pipeline_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/pipeline"
	"oroborus.dev/orotools/internal/pipeline/providers"
	"oroborus.dev/orotools/internal/variables"

	"gopkg.in/yaml.v3"
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
		{"azure-pipelines", ".azure-pipelines/ci.yml"},
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

func TestAzurePipelinesTemplatesFollowStandards(t *testing.T) {
	project := variables.Project{Name: "demo", Slug: "demo", Path: "demo"}
	required := []string{
		"name: $(Date:yyyy.MM.dd).$(Rev:r)",
		"pr: none",
		"name: Default",
		"- main",
		"- dev",
		"persistCredentials: false",
	}
	for _, recipe := range []string{"generic", "dotnet", "dotnet-next", "rails", "fastify-next"} {
		t.Run(recipe, func(t *testing.T) {
			dir := t.TempDir()
			p, err := providers.Get("azure-pipelines")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Generate(context.Background(), project, pipeline.Options{Base: dir, RecipeName: recipe}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "azure-pipelines.yml")); err == nil {
				t.Fatal("pipeline must not be written to the repository root")
			}
			b, err := os.ReadFile(filepath.Join(dir, ".azure-pipelines", "ci.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var parsed map[string]any
			if err := yaml.Unmarshal(b, &parsed); err != nil {
				t.Fatalf("template for %s is not valid YAML: %v", recipe, err)
			}
			for _, want := range required {
				if !strings.Contains(string(b), want) {
					t.Errorf("template for %s is missing %q", recipe, want)
				}
			}
			if strings.Contains(string(b), "vmImage") {
				t.Errorf("template for %s must use the Default pool, not a hosted image", recipe)
			}
		})
	}
}

func TestAzurePipelinesDotnetRecipesPinSDKVersion(t *testing.T) {
	project := variables.Project{Name: "demo", Slug: "demo", Path: "demo"}
	for _, recipe := range []string{"dotnet", "dotnet-next"} {
		t.Run(recipe, func(t *testing.T) {
			dir := t.TempDir()
			p, err := providers.Get("azure-pipelines")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Generate(context.Background(), project, pipeline.Options{Base: dir, RecipeName: recipe}); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, ".azure-pipelines", "ci.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var parsed map[string]any
			if err := yaml.Unmarshal(b, &parsed); err != nil {
				t.Fatalf("invalid YAML: %v", err)
			}
			vars, _ := parsed["variables"].(map[string]any)
			if vars["DOTNET_VERSION"] != "10.x" {
				t.Errorf("DOTNET_VERSION = %v, want 10.x", vars["DOTNET_VERSION"])
			}
			for _, want := range []string{"packageType: sdk", "version: $(DOTNET_VERSION)"} {
				if !strings.Contains(string(b), want) {
					t.Errorf("UseDotNet@2 must set %q explicitly", want)
				}
			}
		})
	}
}

func TestAzurePipelinesRecipesPinNodeVersion(t *testing.T) {
	project := variables.Project{Name: "demo", Slug: "demo", Path: "demo"}
	for _, recipe := range []string{"dotnet-next", "fastify-next"} {
		t.Run(recipe, func(t *testing.T) {
			dir := t.TempDir()
			p, err := providers.Get("azure-pipelines")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Generate(context.Background(), project, pipeline.Options{Base: dir, RecipeName: recipe}); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, ".azure-pipelines", "ci.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var parsed map[string]any
			if err := yaml.Unmarshal(b, &parsed); err != nil {
				t.Fatalf("invalid YAML: %v", err)
			}
			vars, _ := parsed["variables"].(map[string]any)
			if vars["NODE_VERSION"] != "24.x" {
				t.Errorf("NODE_VERSION = %v, want 24.x (recipes demand node >=24)", vars["NODE_VERSION"])
			}
			if !strings.Contains(string(b), "versionSpec: $(NODE_VERSION)") {
				t.Error("NodeTool@0 must install node via $(NODE_VERSION)")
			}
		})
	}
}

func TestAzurePipelinesProviderUsesRecipeTemplate(t *testing.T) {
	dir := t.TempDir()
	recipeFS := fstest.MapFS{
		"pipelines/azure-pipelines.yml.tmpl": &fstest.MapFile{
			Data: []byte("name: custom-azure\nproject: {{.Project.Name}}\n"),
		},
	}
	p, err := providers.Get("azure-pipelines")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Generate(context.Background(), variables.Project{Name: "acme"}, pipeline.Options{
		Base: dir, RecipeName: "test-recipe", RecipeFS: recipeFS,
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".azure-pipelines", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "name: custom-azure\nproject: acme\n" {
		t.Errorf("recipe template was not used by the azure provider: %q", b)
	}
}

func TestAzurePipelinesProviderPreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".azure-pipelines", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := providers.Get("azure-pipelines")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Generate(context.Background(), variables.Project{Name: "demo"}, pipeline.Options{
		Base: dir, RecipeName: "test-recipe",
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "existing" {
		t.Errorf("existing .azure-pipelines/ci.yml was overwritten: %q", b)
	}
}
