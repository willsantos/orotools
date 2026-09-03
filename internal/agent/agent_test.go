package agent_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/agent"
	"oroborus.dev/orotools/internal/recipe"
)

func TestAgentsDir(t *testing.T) {
	got, err := agent.AgentsDir("opencode")
	if err != nil || got != ".opencode/agents" {
		t.Fatalf("AgentsDir = %q, err = %v", got, err)
	}
}

func TestInstallerAlias(t *testing.T) {
	if got := agent.InstallerAlias("opencode", "tech-leads-club"); got != "opencode" {
		t.Errorf("alias = %q", got)
	}
	if got := agent.InstallerAlias("cursor", "unknown"); got != "cursor" {
		t.Errorf("fallback = %q", got)
	}
}

func TestFlattenExternal(t *testing.T) {
	got := agent.FlattenExternal([]recipe.ExternalAgent{
		{Installer: "tech-leads-club"},
		{Installer: "custom", Agents: []string{"reviewer-bot"}},
	})
	want := []string{"tech-leads-club", "reviewer-bot"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FlattenExternal = %v", got)
		}
	}
}

func TestInstallBundled(t *testing.T) {
	dir := t.TempDir()
	recipeFS := fstest.MapFS{
		"agents/architect.md": &fstest.MapFile{Data: []byte("# architect\n")},
	}
	if err := agent.InstallBundled(agent.BundledOptions{
		Base:     dir,
		Agent:    "opencode",
		RecipeFS: recipeFS,
		Bundled:  []string{"architect"},
	}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, ".opencode", "agents", "architect.md")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("agent not copied: %v", err)
	}
}
