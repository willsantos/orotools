package recipes

import (
	"testing"

	"oroborus.dev/orotools/internal/recipe"
)

func TestOfficialRecipesParse(t *testing.T) {
	for _, name := range OfficialNames() {
		t.Run(name, func(t *testing.T) {
			fsys, err := Open(name)
			if err != nil {
				t.Fatal(err)
			}
			r, err := recipe.Load(fsys, RecipeFile)
			if err != nil {
				t.Fatal(err)
			}
			if r.Name != name {
				t.Errorf("name = %q, want %q", r.Name, name)
			}
		})
	}
}

func TestOpenUnknown(t *testing.T) {
	if _, err := Open("unknown"); err == nil {
		t.Fatal("expected error for unknown recipe")
	}
}
