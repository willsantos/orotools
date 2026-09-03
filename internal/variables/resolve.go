package variables

import (
	"errors"
	"fmt"

	"oroborus.dev/orotools/internal/recipe"
)

// Inputs carries user-provided values, separated by source. Precedence is
// applied by Resolve: recipe default < wizard < flags.
type Inputs struct {
	Flags  map[string]any
	Wizard map[string]any
}

// Result is the resolved context consumed by planner/executor/templates.
type Result struct {
	Project Project
	Vars    map[string]any
}

// Resolve derives Project from projectName, resolves every declared variable
// with precedence default<wizard<flags, coerces types, validates select
// options, and rejects unknown inputs. Multiple errors are joined.
func Resolve(r *recipe.Recipe, projectName string, in Inputs) (*Result, error) {
	if r == nil {
		return nil, errors.New("variables: recipe is nil")
	}

	proj, err := deriveProject(projectName)
	if err != nil {
		return nil, err
	}

	var errs []error
	vars := make(map[string]any, len(r.Variables))

	for name, decl := range r.Variables {
		val, has, lookupErr := lookup(name, decl, in)
		if lookupErr != nil {
			errs = append(errs, lookupErr)
			continue
		}
		if !has {
			errs = append(errs, fmt.Errorf("variable %q: no value provided and no default", name))
			continue
		}
		coerced, coerceErr := coerceValue(name, decl, val)
		if coerceErr != nil {
			errs = append(errs, coerceErr)
			continue
		}
		vars[name] = coerced
	}

	errs = append(errs, unknownInputs(r, in)...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &Result{Project: proj, Vars: vars}, nil
}

// lookup returns the value for name following precedence (flag > wizard >
// default). has=false signals no value available (caller reports missing).
func lookup(name string, decl recipe.Variable, in Inputs) (any, bool, error) {
	if v, ok := in.Flags[name]; ok {
		return v, true, nil
	}
	if v, ok := in.Wizard[name]; ok {
		return v, true, nil
	}
	if decl.Default != nil {
		return decl.Default, true, nil
	}
	return nil, false, nil
}

// unknownInputs reports every name present in Flags/Wizard that is not declared
// in the recipe. Catches flag/wizard typos loudly.
func unknownInputs(r *recipe.Recipe, in Inputs) []error {
	declared := r.Variables
	seen := map[string]bool{}
	for k := range in.Flags {
		seen[k] = true
	}
	for k := range in.Wizard {
		seen[k] = true
	}
	var errs []error
	for name := range seen {
		if _, ok := declared[name]; !ok {
			errs = append(errs, fmt.Errorf("variable %q: unknown variable (not declared in recipe)", name))
		}
	}
	return errs
}
