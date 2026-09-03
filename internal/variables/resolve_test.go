package variables

import (
	"errors"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/recipe"
)

func boolPtr(b bool) any    { return b }
func strPtr(s string) any   { return s }

func recipeWith(vars map[string]Variable) *recipe.Recipe {
	out := map[string]recipe.Variable{}
	for k, v := range vars {
		out[k] = v
	}
	return &recipe.Recipe{Version: 1, Name: "x", Variables: out}
}

// Variable is a test-only alias for recipe.Variable to keep test cases terse.
type Variable = recipe.Variable

func TestResolve_DefaultOnly(t *testing.T) {
	r := recipeWith(map[string]Variable{
		"docker": {Type: "boolean", Default: boolPtr(true)},
	})
	res, err := Resolve(r, "Minha API", Inputs{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Vars["docker"] != true {
		t.Errorf("docker = %v, want true", res.Vars["docker"])
	}
	if res.Project.Slug != "minha-api" {
		t.Errorf("slug = %q", res.Project.Slug)
	}
}

func TestResolve_Precedence(t *testing.T) {
	r := recipeWith(map[string]Variable{
		"docker": {Type: "boolean", Default: boolPtr(true)},
	})
	// wizard overrides default
	res, _ := Resolve(r, "p", Inputs{Wizard: map[string]any{"docker": false}})
	if res.Vars["docker"] != false {
		t.Errorf("wizard should win over default: %v", res.Vars["docker"])
	}
	// flag wins over wizard
	res, _ = Resolve(r, "p", Inputs{
		Flags:  map[string]any{"docker": "yes"},
		Wizard: map[string]any{"docker": false},
	})
	if res.Vars["docker"] != true {
		t.Errorf("flag should win over wizard: %v", res.Vars["docker"])
	}
}

func TestResolve_MissingRequiredVariable(t *testing.T) {
	r := recipeWith(map[string]Variable{
		"db": {Type: "string"},
	})
	_, err := Resolve(r, "p", Inputs{})
	if err == nil || !strings.Contains(err.Error(), `"db"`) {
		t.Errorf("expected missing-variable error for db, got %v", err)
	}
}

func TestResolve_UnknownInput(t *testing.T) {
	r := recipeWith(map[string]Variable{
		"db": {Type: "string", Default: strPtr("x")},
	})
	_, err := Resolve(r, "p", Inputs{Flags: map[string]any{"bogus": "y"}})
	if err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Errorf("expected unknown-variable error, got %v", err)
	}
}

func TestResolve_NoVariables(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x"}
	res, err := Resolve(r, "Proj", Inputs{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Vars) != 0 {
		t.Errorf("expected empty Vars, got %v", res.Vars)
	}
}

func TestResolve_MultipleErrorsAccumulated(t *testing.T) {
	r := recipeWith(map[string]Variable{
		"db":  {Type: "string"},                  // missing
		"doc": {Type: "boolean", Default: boolPtr(true)},
	})
	_, err := Resolve(r, "p", Inputs{Flags: map[string]any{"bogus": 1}})
	if err == nil {
		t.Fatal("expected error")
	}
	joined := err.Error()
	if !strings.Contains(joined, `"db"`) {
		t.Errorf("missing db error absent: %s", joined)
	}
	if !strings.Contains(joined, "unknown variable") {
		t.Errorf("unknown input error absent: %s", joined)
	}
	var multi interface{ Unwrap() []error }
	if !errors.As(err, &multi) {
		t.Errorf("error should wrap multiple via Unwrap []error")
	}
}

func TestResolve_SelectOptionValidated(t *testing.T) {
	r := recipeWith(map[string]Variable{
		"db": {Type: "select", Options: []string{"postgres", "sqlite"}},
	})
	_, err := Resolve(r, "p", Inputs{Flags: map[string]any{"db": "mysql"}})
	if err == nil || !strings.Contains(err.Error(), "not in options") {
		t.Errorf("expected options error, got %v", err)
	}
}
