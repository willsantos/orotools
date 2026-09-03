package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/ui"
	"oroborus.dev/orotools/recipes"
)

type recipeSource struct {
	FS       fs.FS
	DirPath  string // filesystem dir when not embedded; empty when embedded
	Embedded bool
}

func resolveRecipe(stack, recipePath string) (recipeSource, error) {
	if recipePath != "" {
		dir := filepath.Dir(recipePath)
		return recipeSource{FS: os.DirFS(dir), DirPath: dir, Embedded: false}, nil
	}
	if stack != "" {
		fsys, err := recipes.Open(stack)
		if err != nil {
			return recipeSource{}, err
		}
		return recipeSource{FS: fsys, Embedded: true}, nil
	}
	return recipeSource{}, fmt.Errorf("either --stack or --recipe is required")
}

func loadRecipeFromSource(src recipeSource, recipePath string) (*recipe.Recipe, error) {
	if src.Embedded {
		return recipe.Load(src.FS, recipes.RecipeFile)
	}
	return loadRecipeFromPath(recipePath)
}

func runRecipeList(out io.Writer) error {
	pal := ui.PaletteFor(out)

	type entry struct{ name, desc string }
	var entries []entry
	nameWidth := 0
	for _, name := range recipes.OfficialNames() {
		fsys, err := recipes.Open(name)
		if err != nil {
			return err
		}
		r, err := recipe.Load(fsys, recipes.RecipeFile)
		if err != nil {
			return err
		}
		entries = append(entries, entry{name, r.Description})
		nameWidth = max(nameWidth, lipgloss.Width(name))
	}

	fmt.Fprintln(out, pal.Title.Render("Recipes oficiais"))
	fmt.Fprintln(out)
	for _, e := range entries {
		nameCell := e.name + strings.Repeat(" ", nameWidth-lipgloss.Width(e.name))
		fmt.Fprintf(out, " %s  %s\n", pal.Key.Render(nameCell), pal.Info.Render(e.desc))
	}
	return nil
}
