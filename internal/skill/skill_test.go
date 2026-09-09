package skill_test

import (
	"testing"

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
