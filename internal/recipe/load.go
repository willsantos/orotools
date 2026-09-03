package recipe

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// ErrValidation indicates that Parse/Load succeeded in decoding the YAML but
// Validate rejected the resulting Recipe. Use errors.Is to distinguish a
// validation failure from a decode or read error.
var ErrValidation = errors.New("recipe validation failed")

// Parse decodes src as a Recipe v1 YAML using strict decoding (unknown fields
// are rejected) and then runs Validate. Returns ErrValidation (wrapped) when
// structural validation fails.
func Parse(src []byte) (*Recipe, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var r Recipe
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse recipe: %w", err)
	}
	if errs := Validate(&r); len(errs) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrValidation, Errors(errs))
	}
	return &r, nil
}

// Load reads a recipe from fsys at path and parses it.
func Load(fsys fs.FS, path string) (*Recipe, error) {
	src, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read recipe %q: %w", path, err)
	}
	return Parse(src)
}

// Errors joins validation error messages with "; ".
func Errors(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return join(parts, "; ")
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}
