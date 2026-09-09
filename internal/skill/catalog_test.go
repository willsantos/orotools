package skill_test

import (
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/skill"
)

func TestRecipeID(t *testing.T) {
	if got, want := skill.RecipeID("dotnet", "base/code-review"), "recipe:dotnet/base/code-review"; got != want {
		t.Errorf("RecipeID = %q, want %q", got, want)
	}
}

func TestGitHubID(t *testing.T) {
	if got, want := skill.GitHubID("local-pr-review"), "github:willsantos/skills_AI/local-pr-review"; got != want {
		t.Errorf("GitHubID = %q, want %q", got, want)
	}
}

func candidate(source skill.SourceKind, name string) skill.Candidate {
	c := skill.Candidate{Name: name, Source: source, Tree: skill.Tree{FS: fstest.MapFS{}}}
	if source == skill.SourceGitHub {
		c.ID = skill.GitHubID(name)
	} else {
		c.ID = skill.RecipeID("dotnet", name)
	}
	return c
}

func TestSortCandidatesBundledBeforeGitHub(t *testing.T) {
	cands := []skill.Candidate{
		candidate(skill.SourceRecipe, "zeta"),
		candidate(skill.SourceGitHub, "remote"),
		candidate(skill.SourceRecipe, "alpha"),
	}
	skill.SortCandidates(cands)
	got := make([]string, 0, len(cands))
	for _, c := range cands {
		got = append(got, c.Name)
	}
	want := []string{"alpha", "zeta", "remote"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestDestinationCollisions(t *testing.T) {
	cands := []skill.Candidate{
		candidate(skill.SourceRecipe, "alpha"),
		candidate(skill.SourceRecipe, "beta"),
		candidate(skill.SourceGitHub, "alpha"),
	}
	if dups := skill.DestinationCollisions(cands); len(dups) != 1 || dups[0] != "alpha" {
		t.Fatalf("DestinationCollisions = %v, want [alpha]", dups)
	}
	if dups := skill.DestinationCollisions(cands[:2]); len(dups) != 0 {
		t.Fatalf("DestinationCollisions = %v, want none", dups)
	}
}
