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

// Manifest models orotools.yaml schema v2 (skills-manager FR-17); the parser
// keeps accepting v1 manifests.
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

// ManagedSkillRef identifies a remote skill chosen for the project (manifest
// schema v2). The installed version is resolved state and belongs to the
// skills lock, not to the desired-state manifest.
type ManagedSkillRef struct {
	Source string `yaml:"source"` // e.g. github:willsantos/skills_AI
	Name   string `yaml:"name"`
}

// SkillsRef lists bundled, external and managed skills for the project.
type SkillsRef struct {
	Bundled  []string          `yaml:"bundled"`
	External []string          `yaml:"external"`
	Managed  []ManagedSkillRef `yaml:"managed,omitempty"`
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

// Parse decodes src as a Manifest using strict decoding. Schema v1 and v2 are
// accepted (skills-manager FR-17).
func Parse(src []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != 1 && m.Version != 2 {
		return nil, fmt.Errorf("manifest version: must be 1 or 2, got %d", m.Version)
	}
	if m.Project.Name == "" {
		return nil, errors.New("manifest: project.name is required")
	}
	if err := validateSkills(m.Skills); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateSkills(s SkillsRef) error {
	seen := make(map[ManagedSkillRef]bool, len(s.Managed))
	for _, ref := range s.Managed {
		if ref.Source == "" || ref.Name == "" {
			return fmt.Errorf("manifest: skills.managed entry needs source and name, got %+v", ref)
		}
		if seen[ref] {
			return fmt.Errorf("manifest: skills.managed has duplicate entry %s/%s", ref.Source, ref.Name)
		}
		seen[ref] = true
	}
	return nil
}

// AddManagedSkill records a remote skill choice, deduping the (source, name)
// pair and promoting the manifest to schema v2 — the promotion happens only
// when a managed reference is persisted (skills-manager FR-15, FR-17).
func (m *Manifest) AddManagedSkill(source, name string) {
	ref := ManagedSkillRef{Source: source, Name: name}
	for _, existing := range m.Skills.Managed {
		if existing == ref {
			return
		}
	}
	m.Skills.Managed = append(m.Skills.Managed, ref)
	if m.Version < 2 {
		m.Version = 2
	}
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
