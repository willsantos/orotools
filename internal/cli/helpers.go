package cli

import (
	"io"
	"os"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/recipe"
)

// manifestFilename is the default manifest path used by commands that operate
// on the current project.
const manifestFilename = manifest.Filename

// loadRecipeFromPath reads a recipe.yaml from a filesystem path (absolute or
// relative). Uses os.ReadFile so both forms work; recipe.Load's fs.FS contract
// rejects absolute paths.
func loadRecipeFromPath(path string) (*recipe.Recipe, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return recipe.Parse(src)
}

// writerOrStdout returns w when set, otherwise os.Stdout.
func writerOrStdout(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stdout
}

// readerOrStdin returns r when set, otherwise os.Stdin.
func readerOrStdin(r io.Reader) io.Reader {
	if r != nil {
		return r
	}
	return os.Stdin
}
