package variables

import (
	"fmt"
	"strings"

	"oroborus.dev/orotools/internal/recipe"
)

// coerceValue converts provided into the declared type of decl.
// name is used only to build clear error messages.
func coerceValue(name string, decl recipe.Variable, provided any) (any, error) {
	switch decl.Type {
	case "boolean":
		return coerceBoolean(name, provided)
	case "string":
		return coerceString(name, provided)
	case "select":
		return coerceSelect(name, decl, provided)
	default:
		return nil, fmt.Errorf("variable %q: unknown type %q", name, decl.Type)
	}
}

func coerceBoolean(name string, v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(x) {
		case "true", "yes", "1":
			return true, nil
		case "false", "no", "0":
			return false, nil
		}
		return false, fmt.Errorf("variable %q: value %q is not a valid boolean (want true|false|yes|no|1|0)", name, x)
	default:
		return false, fmt.Errorf("variable %q: value %v (%T) is not a boolean", name, v, v)
	}
}

func coerceString(name string, v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", fmt.Errorf("variable %q: value %v (%T) is not a string", name, v, v)
}

func coerceSelect(name string, decl recipe.Variable, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("variable %q: value %v (%T) is not a string", name, v, v)
	}
	for _, opt := range decl.Options {
		if s == opt {
			return s, nil
		}
	}
	return "", fmt.Errorf("variable %q: value %q not in options [%s]", name, s, strings.Join(decl.Options, "|"))
}
