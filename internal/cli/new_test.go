package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

type fakeRepoRunner struct{}

func (fakeRepoRunner) LookPath(string) (string, error) { return "git", nil }
func (fakeRepoRunner) Run(context.Context, string, []string, string, io.Writer, io.Writer) error {
	return nil
}

// writeRecipeFixture lays out a recipe.yaml plus a template asset under dir.
func writeRecipeFixture(t *testing.T, dir string) {
	t.Helper()
	recipe := `version: 1
name: test-recipe
description: fixture

variables:
  greeting:
    type: string
    default: hello

steps:
  - id: make-src
    type: mkdir
    path: src
  - id: hello-file
    type: template
    source: templates/hello.txt.tmpl
    destination: hello.txt
  - id: bye
    type: message
    text: "done"
`
	if err := os.WriteFile(filepath.Join(dir, "recipe.yaml"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "hello.txt.tmpl"), []byte("{{.Vars.greeting}} {{.Project.Name}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundledSkillMD returns a SKILL.md body satisfying the versioned metadata
// contract (skills-manager FR-7).
func bundledSkillMD(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\nmetadata:\n  version: \"1.0.0\"\n---\n# " + name + "\n"
}

func writeBundledSkillFixture(t *testing.T, dir string) {
	t.Helper()
	for ref, name := range map[string]string{
		"base/code-review": "code-review",
		"base/formatting":  "formatting",
	} {
		skillDir := filepath.Join(dir, "skills", ref)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(bundledSkillMD(name, name+" guidelines")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunNew_YesMode_CreatesProjectAndManifest(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)

	// cwd where the project will be created
	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	err := runNew(newOptions{
		name:       "meu-app",
		recipePath: filepath.Join(recipeDir, "recipe.yaml"),
		set:        []string{"greeting=oi"},
		yes:        true,
		out:        &out,
		repoRunner: fakeRepoRunner{},
	})
	if err != nil {
		t.Fatalf("runNew: %v\noutput: %s", err, out.String())
	}

	// project dir created
	if _, err := os.Stat(filepath.Join(workDir, "meu-app")); err != nil {
		t.Fatalf("project dir not created: %v", err)
	}
	// src dir created
	if _, err := os.Stat(filepath.Join(workDir, "meu-app", "src")); err != nil {
		t.Errorf("src not created: %v", err)
	}
	// templated file with substitutions
	b, err := os.ReadFile(filepath.Join(workDir, "meu-app", "hello.txt"))
	if err != nil {
		t.Fatalf("hello.txt not created: %v", err)
	}
	if string(b) != "oi meu-app\n" {
		t.Errorf("template render = %q, want %q", b, "oi meu-app\n")
	}
	// manifest written and readable
	manifestPath := filepath.Join(workDir, "meu-app", "orotools.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	// output mentions ready + skipped stages
	if !strings.Contains(out.String(), "Projeto pronto") {
		t.Errorf("output missing Projeto pronto:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "git inicializado") {
		t.Errorf("output missing git status:\n%s", out.String())
	}
}

func TestRunNew_FatalExecutionErrorStopsBeforeReady(t *testing.T) {
	recipeDir := t.TempDir()
	recipe := `version: 1
name: fatal-recipe
description: fixture

steps:
  - id: missing-source
    type: copy
    source: missing.txt
    destination: output.txt
`
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.yaml"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	err := runNew(newOptions{
		name:       "broken-app",
		recipePath: filepath.Join(recipeDir, "recipe.yaml"),
		yes:        true,
		out:        &out,
		repoRunner: fakeRepoRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "erros fatais") {
		t.Fatalf("runNew error = %v, want fatal execution error", err)
	}
	if strings.Contains(out.String(), "Projeto pronto") {
		t.Fatalf("runNew reported success after fatal error:\n%s", out.String())
	}
}

func TestRunNew_PipelineGitHubActions(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)
	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	err := runNew(newOptions{
		name:       "pipe-app",
		recipePath: filepath.Join(recipeDir, "recipe.yaml"),
		yes:        true,
		pipeline:   "github-actions",
		out:        &out,
		repoRunner: fakeRepoRunner{},
	})
	if err != nil {
		t.Fatalf("runNew: %v\noutput: %s", err, out.String())
	}
	wf := filepath.Join(workDir, "pipe-app", ".github", "workflows", "ci.yml")
	if _, err := os.Stat(wf); err != nil {
		t.Fatalf("workflow not created: %v", err)
	}
	if !strings.Contains(out.String(), "pipeline: github-actions") {
		t.Errorf("output missing pipeline success:\n%s", out.String())
	}
	if strings.Contains(out.String(), "pipeline (fase 13)") {
		t.Errorf("pipeline should no longer be skipped:\n%s", out.String())
	}
}

func TestRunNew_PipelineDefaultFromRepository(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)
	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	err := runNew(newOptions{
		name:       "azure-app",
		recipePath: filepath.Join(recipeDir, "recipe.yaml"),
		yes:        true,
		repository: "azure-devops",
		out:        &bytes.Buffer{},
		repoRunner: fakeRepoRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "azure-app", "azure-pipelines.yml")); err != nil {
		t.Fatalf("azure pipeline not created from repository default: %v", err)
	}
}

func writeBundledAgentFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "architect.md"), []byte("# architect\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunNew_BundledAgents(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)
	writeBundledAgentFixture(t, recipeDir)
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	b, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatal(err)
	}
	recipeWithAgents := string(b) + "\nagents:\n  bundled:\n    - architect\n"
	if err := os.WriteFile(recipePath, []byte(recipeWithAgents), 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	if err := runNew(newOptions{
		name:       "agent-app",
		recipePath: recipePath,
		yes:        true,
		ai:         "opencode",
		out:        &out,
		repoRunner: fakeRepoRunner{},
	}); err != nil {
		t.Fatalf("runNew: %v\n%s", err, out.String())
	}
	agentFile := filepath.Join(workDir, "agent-app", ".opencode", "agents", "architect.md")
	if _, err := os.Stat(agentFile); err != nil {
		t.Fatalf("bundled agent not installed: %v", err)
	}
	if strings.Contains(out.String(), "agents (fase 15)") {
		t.Errorf("agents should no longer be skipped:\n%s", out.String())
	}
}

func TestRunNew_BundledSkills(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)
	writeBundledSkillFixture(t, recipeDir)
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	b, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatal(err)
	}
	recipeWithSkills := string(b) + "\nskills:\n  bundled:\n    - base/code-review\n    - base/formatting\n"
	if err := os.WriteFile(recipePath, []byte(recipeWithSkills), 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	if err := runNew(newOptions{
		name:       "skill-app",
		recipePath: recipePath,
		yes:        true,
		ai:         "opencode",
		out:        &out,
		repoRunner: fakeRepoRunner{},
	}); err != nil {
		t.Fatalf("runNew: %v\n%s", err, out.String())
	}
	skillFile := filepath.Join(workDir, "skill-app", ".opencode", "skills", "code-review", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Fatalf("bundled skill not installed: %v", err)
	}
	if strings.Contains(out.String(), "skills (fase 14)") {
		t.Errorf("skills should no longer be skipped:\n%s", out.String())
	}
	// Both bundled skills registered in the lock (skills-manager T12).
	lock, err := skill.ReadLock(filepath.Join(workDir, "skill-app", ".orotools", "skills.lock.yaml"))
	if err != nil {
		t.Fatalf("lock not created by oro new: %v", err)
	}
	if len(lock.Skills) != 2 {
		t.Errorf("lock has %d entries, want 2: %+v", len(lock.Skills), lock.Skills)
	}
	// Scaffold manifest stays v1 (no managed refs in this flow).
	m, err := manifest.Read(filepath.Join(workDir, "skill-app", manifest.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 {
		t.Errorf("scaffold manifest version = %d, want 1", m.Version)
	}
}

func TestRunNew_BundledSkillsPartialFailure(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)
	writeBundledSkillFixture(t, recipeDir)
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	b, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatal(err)
	}
	recipeWithSkills := string(b) + "\nskills:\n  bundled:\n    - base/code-review\n    - base/formatting\n"
	if err := os.WriteFile(recipePath, []byte(recipeWithSkills), 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	// Pre-existing unmanaged destination for one skill: create-only conflict.
	conflictDir := filepath.Join(workDir, "skill-app", ".opencode", "skills", "code-review")
	if err := os.MkdirAll(conflictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflictDir, "user.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runNew(newOptions{
		name:       "skill-app",
		recipePath: recipePath,
		yes:        true,
		ai:         "opencode",
		out:        &out,
		repoRunner: fakeRepoRunner{},
	}); err != nil {
		t.Fatalf("runNew: %v\n%s", err, out.String())
	}
	// Conflicted skill untouched, sibling installed, lock has only the sibling.
	userFile, err := os.ReadFile(filepath.Join(conflictDir, "user.txt"))
	if err != nil || string(userFile) != "keep" {
		t.Errorf("conflicted destination touched: %q, %v", userFile, err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "skill-app", ".opencode", "skills", "formatting", "SKILL.md")); err != nil {
		t.Fatalf("sibling skill not installed: %v", err)
	}
	lock, err := skill.ReadLock(filepath.Join(workDir, "skill-app", ".orotools", "skills.lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Skills) != 1 || !strings.Contains(lock.Skills[0].ID, "formatting") {
		t.Errorf("lock = %+v, want only formatting", lock.Skills)
	}
}

func TestRunNew_DryRun_NoChanges(t *testing.T) {
	recipeDir := t.TempDir()
	writeRecipeFixture(t, recipeDir)
	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	err := runNew(newOptions{
		name:       "dryapp",
		recipePath: filepath.Join(recipeDir, "recipe.yaml"),
		yes:        true,
		dryRun:     true,
		out:        &out,
	})
	if err != nil {
		t.Fatalf("runNew dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "PLAN") || !strings.Contains(out.String(), "Directories") {
		t.Errorf("dry-run output missing plan:\n%s", out.String())
	}
	// nothing created
	if _, err := os.Stat(filepath.Join(workDir, "dryapp")); err == nil {
		t.Errorf("dry-run should not create project dir")
	}
	if _, err := os.Stat(filepath.Join(workDir, "dryapp", "orotools.yaml")); err == nil {
		t.Errorf("dry-run should not write manifest")
	}
}

func TestRunNew_MissingName(t *testing.T) {
	err := runNew(newOptions{recipePath: "x.yaml", yes: true})
	if err == nil || !strings.Contains(err.Error(), "nome") {
		t.Errorf("expected name error, got %v", err)
	}
}

func TestRunNew_MissingStackOrRecipe(t *testing.T) {
	err := runNew(newOptions{name: "x", yes: true})
	if err == nil || !strings.Contains(err.Error(), "--stack") {
		t.Errorf("expected stack/recipe error, got %v", err)
	}
}

func TestParseSetFlags(t *testing.T) {
	got, err := parseSetFlags([]string{"db=postgres", "docker=true", "k="})
	if err != nil {
		t.Fatal(err)
	}
	if got["db"] != "postgres" || got["docker"] != "true" || got["k"] != "" {
		t.Errorf("parseSetFlags = %+v", got)
	}
	if _, err := parseSetFlags([]string{"noequals"}); err == nil {
		t.Errorf("expected error for missing =")
	}
}

// chdir changes to dir and returns a restore func.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(cwd) }
}
