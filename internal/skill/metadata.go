package skill

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// Metadata is the SKILL.md frontmatter contract the catalog relies on
// (skills-manager FR-7). Version is authoritative and must be strict SemVer
// without a "v" prefix.
type Metadata struct {
	Name        string
	Description string
	Version     semver.Version
}

// frontmatter mirrors the YAML block at the top of a SKILL.md. Extra fields in
// the block are tolerated; the contract fields are validated afterwards.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Version string `yaml:"version"`
	} `yaml:"metadata"`
}

// ParseMetadata reads a SKILL.md document and validates its frontmatter
// contract: name, description and metadata.version are mandatory, and version
// must parse as strict SemVer without a "v" prefix (skills-manager FR-7).
func ParseMetadata(r io.Reader) (Metadata, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return Metadata{}, fmt.Errorf("read skill: %w", err)
	}
	front, err := frontmatterBlock(src)
	if err != nil {
		return Metadata{}, err
	}
	var f frontmatter
	if err := yaml.Unmarshal(front, &f); err != nil {
		return Metadata{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	if f.Name == "" {
		return Metadata{}, fmt.Errorf("skill frontmatter: name is required")
	}
	if !cleanSegment(f.Name) {
		return Metadata{}, fmt.Errorf("skill frontmatter: name %q não é um nome de diretório válido", f.Name)
	}
	if f.Description == "" {
		return Metadata{}, fmt.Errorf("skill frontmatter: description is required")
	}
	raw := f.Metadata.Version
	if raw == "" {
		return Metadata{}, fmt.Errorf("skill frontmatter: metadata.version is required")
	}
	if strings.HasPrefix(raw, "v") {
		return Metadata{}, fmt.Errorf("skill frontmatter: metadata.version %q must not carry a %q prefix", raw, "v")
	}
	v, err := semver.StrictNewVersion(raw)
	if err != nil {
		return Metadata{}, fmt.Errorf("skill frontmatter: metadata.version %q is not valid SemVer: %w", raw, err)
	}
	return Metadata{Name: f.Name, Description: f.Description, Version: *v}, nil
}

// frontmatterBlock extracts the leading "---" fenced YAML block from a
// SKILL.md document. The body after the closing fence is ignored here.
func frontmatterBlock(src []byte) ([]byte, error) {
	rest, ok := bytes.CutPrefix(src, []byte("---\n"))
	if !ok {
		return nil, fmt.Errorf("skill: document has no frontmatter")
	}
	raw, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		raw, ok = bytes.CutSuffix(rest, []byte("\n---"))
		if !ok {
			return nil, fmt.Errorf("skill: frontmatter is not closed")
		}
	}
	return raw, nil
}
