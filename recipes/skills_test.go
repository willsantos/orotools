package recipes

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"

	"oroborus.dev/orotools/internal/recipe"
)

// skillFrontmatter mirrors the part of the SKILL.md frontmatter contract the
// catalog relies on (skills-manager FR-7): name, description and
// metadata.version as unprefixed SemVer.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Version string `yaml:"version"`
	} `yaml:"metadata"`
}

// TestBundledSkillsHaveVersionedMetadata audits every bundled ref of the
// official recipes: each must resolve to a directory with a SKILL.md whose
// frontmatter carries name, description and a valid metadata.version
// (skills-manager T1 / FR-7).
func TestBundledSkillsHaveVersionedMetadata(t *testing.T) {
	for _, name := range OfficialNames() {
		t.Run(name, func(t *testing.T) {
			fsys, err := Open(name)
			if err != nil {
				t.Fatal(err)
			}
			r, err := recipe.Load(fsys, RecipeFile)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Skills.Bundled) == 0 {
				t.Fatalf("recipe %q declares no bundled skills; want at least one (audit would silently pass)", name)
			}
			for _, ref := range r.Skills.Bundled {
				path := "skills/" + ref + "/SKILL.md"
				src, err := fs.ReadFile(fsys, path)
				if err != nil {
					t.Errorf("skill %q: %v", ref, err)
					continue
				}
				var md skillFrontmatter
				if err := decodeFrontmatter(src, &md); err != nil {
					t.Errorf("skill %q: %v", ref, err)
					continue
				}
				if md.Name == "" {
					t.Errorf("skill %q: frontmatter name is required", ref)
				}
				if md.Description == "" {
					t.Errorf("skill %q: frontmatter description is required", ref)
				}
				v := md.Metadata.Version
				switch {
				case v == "":
					t.Errorf("skill %q: metadata.version is required", ref)
				case strings.HasPrefix(v, "v"):
					t.Errorf("skill %q: metadata.version %q must not carry the %q prefix", ref, v, "v")
				default:
					if _, err := semver.NewVersion(v); err != nil {
						t.Errorf("skill %q: metadata.version %q is not valid SemVer: %v", ref, v, err)
					}
				}
			}
		})
	}
}

// decodeFrontmatter extracts the leading `---` fenced YAML block of a SKILL.md
// and decodes it into out.
func decodeFrontmatter(src []byte, out *skillFrontmatter) error {
	rest, ok := bytes.CutPrefix(src, []byte("---\n"))
	if !ok {
		return errors.New("SKILL.md has no frontmatter")
	}
	raw, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		raw, _, ok = bytes.Cut(rest, []byte("\n---"))
		if !ok {
			return errors.New("SKILL.md frontmatter is not closed")
		}
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid frontmatter: %w", err)
	}
	return nil
}
