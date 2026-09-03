// Package recipes exposes official embedded recipe assets.
package recipes

import (
	"fmt"
	"io/fs"
	"sort"

	dotnet "oroborus.dev/orotools/recipes/dotnet"
	dotnetnext "oroborus.dev/orotools/recipes/dotnet-next"
	fastifynext "oroborus.dev/orotools/recipes/fastify-next"
	"oroborus.dev/orotools/recipes/rails"
)

// RecipeFile is the canonical recipe filename inside each embedded recipe FS.
const RecipeFile = "recipe.yaml"

var official = map[string]fs.FS{
	"dotnet":       dotnet.FS,
	"dotnet-next":  dotnetnext.FS,
	"fastify-next": fastifynext.FS,
	"rails":        rails.FS,
}

// OfficialNames returns sorted official recipe slugs.
func OfficialNames() []string {
	out := make([]string, 0, len(official))
	for name := range official {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Open returns the embedded filesystem for an official recipe slug.
func Open(name string) (fs.FS, error) {
	fsys, ok := official[name]
	if !ok {
		return nil, fmt.Errorf("unknown official recipe %q (want one of: %s)", name, joinNames())
	}
	return fsys, nil
}

func joinNames() string {
	return fmt.Sprintf("%v", OfficialNames())
}
