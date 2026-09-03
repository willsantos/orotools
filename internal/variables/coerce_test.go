package variables

import (
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/recipe"
)

func TestCoerceBoolean(t *testing.T) {
	decl := recipe.Variable{Type: "boolean"}
	cases := []struct {
		in     any
		want   bool
		wantErr bool
	}{
		{true, true, false},
		{false, false, false},
		{"true", true, false},
		{"FALSE", false, false},
		{"yes", true, false},
		{"No", false, false},
		{"1", true, false},
		{"0", false, false},
		{"maybe", false, true},
		{42, false, true},
	}
	for _, c := range cases {
		got, err := coerceValue("docker", decl, c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("coerce(boolean, %v) expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("coerce(boolean, %v) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("coerce(boolean, %v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCoerceString(t *testing.T) {
	decl := recipe.Variable{Type: "string"}
	if got, err := coerceValue("note", decl, "hi"); err != nil || got != "hi" {
		t.Errorf("coerce(string, hi) = %v, %v", got, err)
	}
	if _, err := coerceValue("note", decl, 42); err == nil {
		t.Errorf("coerce(string, 42) expected error")
	}
}

func TestCoerceSelect(t *testing.T) {
	decl := recipe.Variable{Type: "select", Options: []string{"postgres", "sqlite"}}
	if got, err := coerceValue("db", decl, "postgres"); err != nil || got != "postgres" {
		t.Errorf("coerce(select, postgres) = %v, %v", got, err)
	}
	_, err := coerceValue("db", decl, "mysql")
	if err == nil || !strings.Contains(err.Error(), "not in options") {
		t.Errorf("coerce(select, mysql) expected options error, got %v", err)
	}
	_, err = coerceValue("db", decl, 42)
	if err == nil {
		t.Errorf("coerce(select, 42) expected error")
	}
}

func TestCoerceUnknownType(t *testing.T) {
	decl := recipe.Variable{Type: "magic"}
	if _, err := coerceValue("x", decl, "anything"); err == nil {
		t.Errorf("expected error for unknown type")
	}
}
