package skill_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/skill"
)

func TestSkillsDir(t *testing.T) {
	got, err := skill.SkillsDir("opencode")
	if err != nil || got != ".opencode/skills" {
		t.Fatalf("SkillsDir = %q, err = %v", got, err)
	}
	if _, err := skill.SkillsDir("unknown"); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestFlattenExternal(t *testing.T) {
	got := skill.FlattenExternal([]recipe.ExternalSkill{
		{Installer: "impeccable"},
		{Installer: "tech-leads-club", Skills: []string{"tlc-spec-driven", "security-best-practices"}},
	})
	want := []string{"impeccable", "tlc-spec-driven", "security-best-practices"}
	if len(got) != len(want) {
		t.Fatalf("FlattenExternal = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FlattenExternal = %v, want %v", got, want)
		}
	}
}

func TestInstallBundled(t *testing.T) {
	dir := t.TempDir()
	recipeFS := fstest.MapFS{
		"skills/base/code-review/SKILL.md": &fstest.MapFile{Data: []byte("# review\n")},
	}
	if err := skill.InstallBundled(skill.BundledOptions{
		Base:     dir,
		Agent:    "opencode",
		RecipeFS: recipeFS,
		Bundled:  []string{"base/code-review"},
	}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, ".opencode", "skills", "code-review", "SKILL.md")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("skill not copied: %v", err)
	}
}

func TestInstallBundledSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	destDir := filepath.Join(dir, ".opencode", "skills", "code-review")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "SKILL.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	recipeFS := fstest.MapFS{
		"skills/base/code-review/SKILL.md": &fstest.MapFile{Data: []byte("# new\n")},
	}
	if err := skill.InstallBundled(skill.BundledOptions{
		Base:     dir,
		Agent:    "opencode",
		RecipeFS: recipeFS,
		Bundled:  []string{"base/code-review"},
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(destDir, "SKILL.md"))
	if string(b) != "existing" {
		t.Errorf("existing skill overwritten: %q", b)
	}
}
