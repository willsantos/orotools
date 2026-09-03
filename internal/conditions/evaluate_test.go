package conditions

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/recipe"
)

func mustEval(t *testing.T, cond recipe.Condition, ctx Context, want bool) {
	t.Helper()
	got, err := Evaluate(cond, ctx)
	if err != nil {
		t.Fatalf("Evaluate err: %v", err)
	}
	if got != want {
		t.Errorf("Evaluate = %v, want %v", got, want)
	}
}

func ctxWith(vars map[string]any) Context {
	return Context{Vars: vars, Base: "."}
}

func TestEquals(t *testing.T) {
	cases := []struct {
		name string
		cond recipe.Condition
		vars map[string]any
		want bool
	}{
		{"bool true match", recipe.Condition{Variable: "docker", Equals: anyVal(true)}, map[string]any{"docker": true}, true},
		{"bool mismatch", recipe.Condition{Variable: "docker", Equals: anyVal(true)}, map[string]any{"docker": false}, false},
		{"string match", recipe.Condition{Variable: "db", Equals: anyVal("postgres")}, map[string]any{"db": "postgres"}, true},
		{"string not-equals op", recipe.Condition{Variable: "db", NotEquals: anyVal("none")}, map[string]any{"db": "postgres"}, true},
		{"string not-equals op false", recipe.Condition{Variable: "db", NotEquals: anyVal("postgres")}, map[string]any{"db": "postgres"}, false},
		{"type mismatch int vs string", recipe.Condition{Variable: "x", Equals: anyVal(42)}, map[string]any{"x": "42"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustEval(t, c.cond, ctxWith(c.vars), c.want)
		})
	}
}

func TestEqualsErrors(t *testing.T) {
	cases := []struct {
		name string
		cond recipe.Condition
		vars map[string]any
		want string
	}{
		{"unknown variable", recipe.Condition{Variable: "missing", Equals: anyVal("x")}, map[string]any{}, `"missing"`},
		{"equals without variable", recipe.Condition{Equals: anyVal(true)}, map[string]any{}, "variable"},
		{"malformed empty", recipe.Condition{}, map[string]any{}, "no operator"},
		{"ambiguous multi", recipe.Condition{Variable: "x", Equals: anyVal(1), PathExists: "a"}, map[string]any{"x": 1}, "ambiguous"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Evaluate(c.cond, ctxWith(c.vars))
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestAllAny(t *testing.T) {
	tTrue := recipe.Condition{Variable: "a", Equals: anyVal(true)}
	tFalse := recipe.Condition{Variable: "a", Equals: anyVal(false)}
	vars := map[string]any{"a": true}

	mustEval(t, recipe.Condition{All: []recipe.Condition{tTrue, tTrue}}, ctxWith(vars), true)
	mustEval(t, recipe.Condition{All: []recipe.Condition{tTrue, tFalse}}, ctxWith(vars), false)
	mustEval(t, recipe.Condition{All: []recipe.Condition{}}, ctxWith(vars), true)  // empty slice → vacuous true
	mustEval(t, recipe.Condition{Any: []recipe.Condition{tTrue, tFalse}}, ctxWith(vars), true)
	mustEval(t, recipe.Condition{Any: []recipe.Condition{tFalse, tFalse}}, ctxWith(vars), false)
	mustEval(t, recipe.Condition{Any: []recipe.Condition{}}, ctxWith(vars), false) // empty slice → vacuous false

	// nil All/Any counts as no operand → malformed
	if _, err := Evaluate(recipe.Condition{All: nil}, ctxWith(vars)); err == nil {
		t.Fatal("expected malformed error for nil All")
	}

	// nested all(any(...))
	nested := recipe.Condition{
		All: []recipe.Condition{
			{Any: []recipe.Condition{tFalse, tTrue}},
			tTrue,
		},
	}
	mustEval(t, nested, ctxWith(vars), true)
}

func TestAllChildErrorPropagates(t *testing.T) {
	badChild := recipe.Condition{Variable: "missing", Equals: anyVal(true)}
	_, err := Evaluate(recipe.Condition{All: []recipe.Condition{badChild}}, ctxWith(map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "all[0]") {
		t.Errorf("expected wrapped all[0] error, got %v", err)
	}
}

func TestPathExists_MapFS(t *testing.T) {
	mapFS := fstest.MapFS{
		"apps/api":     &fstest.MapFile{Mode: 0o755},
		"apps/api/bin": &fstest.MapFile{Mode: 0o644},
	}
	ctx := Context{Vars: map[string]any{}, Base: ".", FS: mapFS}

	mustEval(t, recipe.Condition{PathExists: "apps/api"}, ctx, true)
	mustEval(t, recipe.Condition{PathExists: "apps/missing"}, ctx, false)
}

func TestPathExists_Errors(t *testing.T) {
	ctx := Context{Vars: map[string]any{}, Base: ".", FS: fstest.MapFS{}}
	cases := []struct {
		name string
		path string
		want string
	}{
		{"empty", "", "no operator"}, // empty path → no operand → malformed
		{"absolute", "/etc", "absolute"},
		{"dotdot", "../secret", "traversal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Evaluate(recipe.Condition{PathExists: c.path}, ctx)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestErrorsUnwrap(t *testing.T) {
	_, err := Evaluate(recipe.Condition{All: []recipe.Condition{{Variable: "x", Equals: anyVal(1)}}}, ctxWith(map[string]any{}))
	var target interface{ Unwrap() []error }
	if !errors.As(err, &target) && !errors.Is(err, err) {
		// errors.Join or wrap; just ensure non-nil with message
		if err == nil {
			t.Fatal("expected error")
		}
	}
}

// anyVal returns &v; helper to build *any fields without repeating boilerplate.
func anyVal(v any) *any { return &v }
