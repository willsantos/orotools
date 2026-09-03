package cli

import (
	"fmt"

	"github.com/charmbracelet/huh"

	"oroborus.dev/orotools/internal/recipe"
)

// promptVariables interactively asks the user for every variable in decls that
// is not already present in provided. Defaults are used as initial values.
// Returns the merged map (provided + answers).
func promptVariables(decls map[string]recipe.Variable, provided map[string]any) (map[string]any, error) {
	merged := map[string]any{}
	for k, v := range provided {
		merged[k] = v
	}

	fields, captures := buildFields(decls, provided)
	if len(fields) == 0 {
		return merged, nil
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
		return nil, fmt.Errorf("wizard: %w", err)
	}
	for _, c := range captures {
		switch c.kind {
		case "string":
			merged[c.name] = *c.strPtr
		case "bool":
			merged[c.name] = *c.boolPtr
		}
	}
	return merged, nil
}

type capture struct {
	name    string
	kind    string // "string" | "bool"
	strPtr  *string
	boolPtr *bool
}

func buildFields(decls map[string]recipe.Variable, provided map[string]any) ([]huh.Field, []capture) {
	fields := make([]huh.Field, 0, len(decls))
	var captures []capture

	for name, decl := range decls {
		if _, ok := provided[name]; ok {
			continue
		}
		title := decl.Prompt
		if title == "" {
			title = name
		}
		switch decl.Type {
		case "boolean":
			def := false
			if b, ok := decl.Default.(bool); ok {
				def = b
			}
			val := def
			fields = append(fields, huh.NewConfirm().
				Title(title).
				Value(&val).
				Affirmative("Yes").
				Negative("No"))
			captures = append(captures, capture{name: name, kind: "bool", boolPtr: &val})
		case "select":
			opts := make([]huh.Option[string], 0, len(decl.Options))
			for _, o := range decl.Options {
				opts = append(opts, huh.NewOption(o, o))
			}
			def := ""
			if s, ok := decl.Default.(string); ok {
				def = s
			}
			val := def
			fields = append(fields, huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(&val))
			captures = append(captures, capture{name: name, kind: "string", strPtr: &val})
		default: // string
			def, _ := decl.Default.(string)
			val := def
			fields = append(fields, huh.NewInput().
				Title(title).
				Value(&val).
				Placeholder(def))
			captures = append(captures, capture{name: name, kind: "string", strPtr: &val})
		}
	}
	return fields, captures
}
