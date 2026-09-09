package skill_test

import (
	"context"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/skill"
)

func skillDoc(name, description, version string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\nmetadata:\n  version: \"" + version + "\"\n---\n# " + name + "\n"
}

func TestRecipeCatalogListsOnlyDeclaredRefs(t *testing.T) {
	recipeFS := fstest.MapFS{
		"skills/base/code-review/SKILL.md":    &fstest.MapFile{Data: []byte(skillDoc("code-review", "Review guidelines", "1.0.0"))},
		"skills/dotnet/aspnet/SKILL.md":       &fstest.MapFile{Data: []byte(skillDoc("aspnet", "ASP.NET Core conventions", "1.1.0"))},
		"skills/other-recipe/secret/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("secret", "Must not leak", "1.0.0"))},
	}
	cat := skill.RecipeCatalog{RecipeName: "dotnet", FS: recipeFS, Refs: []string{"dotnet/aspnet", "base/code-review"}}
	cands, warns, err := cat.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}
	// Deterministic order: bundled sorted by name.
	if cands[0].Name != "aspnet" || cands[1].Name != "code-review" {
		t.Fatalf("order = [%s, %s], want [aspnet code-review]", cands[0].Name, cands[1].Name)
	}
	first := cands[0]
	if first.ID != "recipe:dotnet/dotnet/aspnet" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Version.String() != "1.1.0" || first.Description != "ASP.NET Core conventions" {
		t.Errorf("candidate = %+v", first)
	}
	if first.Tree.Root != "skills/dotnet/aspnet" {
		t.Errorf("Tree.Root = %q", first.Tree.Root)
	}
	if first.Source != skill.SourceRecipe || first.SourceRef != "dotnet" || first.Revision != "" {
		t.Errorf("source fields = %+v", first)
	}
}

func TestRecipeCatalogRefMissing(t *testing.T) {
	recipeFS := fstest.MapFS{}
	cat := skill.RecipeCatalog{RecipeName: "dotnet", FS: recipeFS, Refs: []string{"base/missing"}}
	if _, _, err := cat.List(context.Background()); err == nil {
		t.Fatal("expected error for missing ref")
	}
}

func TestRecipeCatalogInvalidMetadataIsFatal(t *testing.T) {
	recipeFS := fstest.MapFS{
		"skills/base/code-review/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: code-review\n---\n")},
	}
	cat := skill.RecipeCatalog{RecipeName: "dotnet", FS: recipeFS, Refs: []string{"base/code-review"}}
	cands, warns, err := cat.List(context.Background())
	if err == nil {
		t.Fatal("expected fatal error for invalid bundled metadata")
	}
	if len(cands) != 0 || len(warns) != 0 {
		t.Fatalf("bundled failure must not degrade to warning; cands=%d warns=%d", len(cands), len(warns))
	}
}

func TestRecipeCatalogDestinationCollision(t *testing.T) {
	recipeFS := fstest.MapFS{
		"skills/a/one/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("same-name", "d", "1.0.0"))},
		"skills/b/two/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("same-name", "d", "1.0.0"))},
	}
	cat := skill.RecipeCatalog{RecipeName: "r", FS: recipeFS, Refs: []string{"a/one", "b/two"}}
	if _, _, err := cat.List(context.Background()); err == nil {
		t.Fatal("expected error for colliding destinations")
	}
}
