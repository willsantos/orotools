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
