package skill_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

func TestLocalCatalogRequiresValidFrontmatterAndStableIdentity(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "docs-writer")
	if err := os.MkdirAll(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valid, "SKILL.md"), []byte(skillDoc("docs-writer", "Write docs", "1.2.3")), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "wrong-name")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte(skillDoc("different", "Wrong", "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := skill.LocalCatalog{Root: root, SourceRef: "checkout-0123456789abcdef", Revision: "abcdef"}
	candidates, warnings, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Source != skill.SourceLocal || candidates[0].Revision != "abcdef" || candidates[0].ID != skill.LocalID(catalog.SourceRef, "docs-writer") {
		t.Fatalf("candidates = %+v", candidates)
	}
	if len(warnings) != 1 || warnings[0].SkillID != skill.LocalID(catalog.SourceRef, "wrong-name") {
		t.Fatalf("warnings = %+v", warnings)
	}
	if _, _, err := (skill.LocalCatalog{Root: root}).List(context.Background()); err == nil {
		t.Fatal("catalog without source identity accepted")
	}
}

func TestLocalCatalogDoesNotFollowSkillDirectorySymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(skillDoc("escape", "Escape", "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	candidates, _, err := (skill.LocalCatalog{Root: root, SourceRef: "test"}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("symlink catalog entries followed: %+v", candidates)
	}
}

func TestLocalCatalogDiscoversAuthoringSkillsLayout(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"docs-writer", "pipeline-helper"} {
		dir := filepath.Join(root, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillDoc(name, "Does "+name, "1.0.0")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "wrong-name"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "wrong-name", "SKILL.md"), []byte(skillDoc("different", "Wrong", "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	// A nested skills/skills directory must not recurse.
	if err := os.MkdirAll(filepath.Join(root, "skills", "skills", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "skills", "nested", "SKILL.md"), []byte(skillDoc("nested", "Nested", "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := skill.LocalCatalog{Root: root, SourceRef: "checkout-0123456789abcdef", Revision: "abc123"}
	candidates, warnings, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	byName := map[string]skill.Candidate{}
	for _, c := range candidates {
		byName[c.Name] = c
	}
	for _, name := range []string{"docs-writer", "pipeline-helper"} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("missing candidate %q in %+v", name, candidates)
		}
		if c.Path != "skills/"+name {
			t.Fatalf("candidate %q path = %q", name, c.Path)
		}
		if c.ID != skill.LocalID(catalog.SourceRef, name) || c.Source != skill.SourceLocal || c.Revision != "abc123" {
			t.Fatalf("candidate %q = %+v", name, c)
		}
	}
	if len(warnings) != 1 || warnings[0].SkillID != skill.LocalID(catalog.SourceRef, "wrong-name") {
		t.Fatalf("warnings = %+v", warnings)
	}
}

func TestLocalCatalogSkillsDirSymlinkIsIgnored(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "escape"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "escape", "SKILL.md"), []byte(skillDoc("escape", "Escape", "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "skills")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	candidates, _, err := (skill.LocalCatalog{Root: root, SourceRef: "test"}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("symlinked skills/ directory followed: %+v", candidates)
	}
}

func TestLocalCatalogRootAndAuthoringCollisionFailsClosed(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{filepath.Join(root, "dup"), filepath.Join(root, "skills", "dup")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillDoc("dup", "Duplicate", "1.0.0")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := (skill.LocalCatalog{Root: root, SourceRef: "test"}).List(context.Background())
	if err == nil {
		t.Fatal("expected conflict between root and skills/ layouts")
	}
}

func TestLocalCatalogSkillsFileIsIgnored(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "skills"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidates, _, err := (skill.LocalCatalog{Root: root, SourceRef: "test"}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v", candidates)
	}
}
