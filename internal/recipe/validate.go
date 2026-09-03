package recipe

import (
	"fmt"
)

// Known enum values for the v1 schema.
var (
	knownStepTypes     = map[string]bool{"mkdir": true, "copy": true, "template": true, "exec": true, "message": true}
	knownVariableTypes = map[string]bool{"string": true, "boolean": true, "select": true}
	knownTemplateModes = map[string]bool{"": true, "create": true, "overwrite": true}
)

// Validate checks the structural integrity of r and returns every error found.
// An empty slice means the recipe is structurally valid. Unknown YAML keys and
// syntactic problems are caught earlier by the strict decoder in Parse; this
// function only checks semantic-structural rules over decoded fields.
func Validate(r *Recipe) []error {
	if r == nil {
		return []error{fmt.Errorf("recipe: nil")}
	}
	var errs []error

	if r.Version != 1 {
		errs = append(errs, fmt.Errorf("version: must be 1, got %d", r.Version))
	}
	if r.Name == "" {
		errs = append(errs, fmt.Errorf("name: required field is empty"))
	}

	for name, v := range r.Variables {
		errs = append(errs, validateVariable("variables["+name+"]", v)...)
	}

	for i, s := range r.Steps {
		errs = append(errs, validateStep(fmt.Sprintf("steps[%d]", i), s)...)
	}

	for i, req := range r.Requirements {
		prefix := fmt.Sprintf("requirements[%d]", i)
		if req.Command == "" {
			errs = append(errs, fmt.Errorf("%s.command: required field is empty", prefix))
		}
		if req.When != nil {
			errs = append(errs, validateCondition(prefix+".when", *req.When)...)
		}
	}

	for name, inst := range r.ExternalInstallers {
		prefix := "external_installers[" + name + "]"
		if inst.Runtime == "" {
			errs = append(errs, fmt.Errorf("%s.runtime: required field is empty", prefix))
		}
		if inst.Package.Name == "" {
			errs = append(errs, fmt.Errorf("%s.package.name: required field is empty", prefix))
		}
	}

	return errs
}

func validateVariable(path string, v Variable) []error {
	var errs []error
	if !knownVariableTypes[v.Type] {
		errs = append(errs, fmt.Errorf("%s.type: unknown %q (want string|boolean|select)", path, v.Type))
	}
	if v.Type == "select" && len(v.Options) == 0 {
		errs = append(errs, fmt.Errorf("%s.options: required and non-empty when type is select", path))
	}
	return errs
}

func validateStep(path string, s Step) []error {
	var errs []error

	if s.Type == "" {
		errs = append(errs, fmt.Errorf("%s.type: required field is empty", path))
	} else if !knownStepTypes[s.Type] {
		errs = append(errs, fmt.Errorf("%s.type: unknown %q (want mkdir|copy|template|exec|message)", path, s.Type))
	}

	if !knownTemplateModes[s.Mode] {
		errs = append(errs, fmt.Errorf("%s.mode: unknown %q (want create|overwrite)", path, s.Mode))
	}

	switch s.Type {
	case "mkdir":
		if s.Path == "" {
			errs = append(errs, fmt.Errorf("%s.path: required for type %q", path, s.Type))
		}
	case "copy":
		if s.Source == "" {
			errs = append(errs, fmt.Errorf("%s.source: required for type %q", path, s.Type))
		}
		if s.Destination == "" {
			errs = append(errs, fmt.Errorf("%s.destination: required for type %q", path, s.Type))
		}
	case "template":
		if s.Source == "" {
			errs = append(errs, fmt.Errorf("%s.source: required for type %q", path, s.Type))
		}
		if s.Destination == "" {
			errs = append(errs, fmt.Errorf("%s.destination: required for type %q", path, s.Type))
		}
	case "exec":
		if s.Command == "" {
			errs = append(errs, fmt.Errorf("%s.command: required for type %q", path, s.Type))
		}
	case "message":
		if s.Text == "" {
			errs = append(errs, fmt.Errorf("%s.text: required for type %q", path, s.Type))
		}
	}

	if s.When != nil {
		errs = append(errs, validateCondition(path+".when", *s.When)...)
	}
	if s.SkipIf != nil {
		errs = append(errs, validateCondition(path+".skip_if", *s.SkipIf)...)
	}
	return errs
}

// validateCondition checks semantic-structural rules over a decoded Condition.
// Unknown keys are already rejected by the strict YAML decoder, so we only
// assert that operator clauses (equals/not_equals) carry a variable target.
func validateCondition(path string, c Condition) []error {
	var errs []error

	hasOp := c.Equals != nil || c.NotEquals != nil
	if hasOp && c.Variable == "" {
		errs = append(errs, fmt.Errorf("%s.variable: required when equals/not_equals is set", path))
	}

	for i, child := range c.All {
		errs = append(errs, validateCondition(fmt.Sprintf("%s.all[%d]", path, i), child)...)
	}
	for i, child := range c.Any {
		errs = append(errs, validateCondition(fmt.Sprintf("%s.any[%d]", path, i), child)...)
	}
	return errs
}
