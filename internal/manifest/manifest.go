package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Filename is the canonical manifest filename in a project root.
const Filename = "orotools.yaml"

// Header is written as a comment at the top of every written manifest.
const Header = "# orotools.yaml — desired state managed by Orotools. Edit with care."

// Manifest models orotools.yaml v1 (spec section 26).
type Manifest struct {
	Version    int            `yaml:"version"`
	Recipe     RecipeRef      `yaml:"recipe"`
	Project    ProjectRef     `yaml:"project"`
	Variables  map[string]any `yaml:"variables"`
	Repository ProviderRef    `yaml:"repository"`
	Pipeline   ProviderRef    `yaml:"pipeline"`
	AI         AIRef          `yaml:"ai"`
	Skills     SkillsRef      `yaml:"skills"`
	Agents     AgentsRef      `yaml:"agents"`
}

// RecipeRef references the recipe that produced this project.
type RecipeRef struct {
	Name    string `yaml:"name"`
	Version int    `yaml:"version"`
}

// ProjectRef carries the project identity.
type ProjectRef struct {
	Name string `yaml:"name"`
}

// ProviderRef identifies a repository or pipeline provider.
type ProviderRef struct {
	Provider string `yaml:"provider"`
}

// AIRef identifies the configured AI agent target.
type AIRef struct {
	Agent string `yaml:"agent"`
}

// SkillsRef lists bundled and external skills for the project.
type SkillsRef struct {
	Bundled  []string `yaml:"bundled"`
	External []string `yaml:"external"`
}

// AgentsRef lists bundled and external agents for the project.
type AgentsRef struct {
	Bundled  []string `yaml:"bundled"`
	External []string `yaml:"external"`
}

// Default returns a minimal valid Manifest (version 1, nothing else set).
func Default() *Manifest {
	return &Manifest{Version: 1}
}

// Read parses the manifest at path. Returns a wrapped error when the file is
// missing, syntactically invalid, or contains unknown fields (strict).
func Read(path string) (*Manifest, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}
	return Parse(src)
}

// Parse decodes src as a Manifest v1 using strict decoding.
func Parse(src []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest version: must be 1, got %d", m.Version)
	}
	if m.Project.Name == "" {
		return nil, errors.New("manifest: project.name is required")
	}
	return &m, nil
}

// Write serialises m to path with a leading comment header. It overwrites any
// existing file (the manifest is the authoritative desired state).
func Write(path string, m *Manifest) error {
	if m == nil {
		return errors.New("manifest: nothing to write (nil)")
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	content := Header + "\n" + string(out)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write manifest %q: %w", path, err)
	}
	return nil
}
