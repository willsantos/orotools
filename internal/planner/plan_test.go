package planner

import (
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

func boolAny(v bool) *any { x := any(v); return &x }

func resWith(name string, vars map[string]any) *variables.Result {
	return &variables.Result{
		Project: variables.Project{Name: name, Slug: name, Path: "./" + name},
		Vars:    vars,
	}
}

func TestBuild_WhenFilter(t *testing.T) {
	r := &recipe.Recipe{
		Version: 1, Name: "x",
		Variables: map[string]recipe.Variable{
			"docker": {Type: "boolean", Default: boolAny(false)},
		},
		Steps: []recipe.Step{
			{ID: "always", Type: "mkdir", Path: "src"},
			{ID: "cond-on", Type: "mkdir", Path: "docker-dir", When: &recipe.Condition{Variable: "docker", Equals: boolAny(true)}},
			{ID: "cond-off", Type: "mkdir", Path: "skip-me", When: &recipe.Condition{Variable: "docker", Equals: boolAny(false)}},
		},
	}
	res := resWith("p", map[string]any{"docker": false})
	ctx := conditions.Context{Vars: res.Vars}

	p, err := Build(r, res, ctx, "opencode")
	if err != nil {
		t.Fatalf("Build err: %v", err)
	}
	// "always" + "cond-off" (docker==false matches) survive; "cond-on" filtered
	if len(p.Directories) != 2 {
		t.Errorf("Directories = %v, want 2 entries", p.Directories)
	}
	if contains(p.Directories, "skip-me") != true {
		t.Errorf(`expected "skip-me" included when docker==false`)
	}
	if contains(p.Directories, "docker-dir") {
		t.Errorf(`"docker-dir" should be filtered out when docker==false`)
	}
}

func TestBuild_WhenError(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{
		{ID: "bad", Type: "mkdir", Path: "x", When: &recipe.Condition{Variable: "missing", Equals: boolAny(true)}},
	}}
	_, err := Build(r, resWith("p", map[string]any{}), conditions.Context{Vars: map[string]any{}}, "opencode")
	if err == nil || !strings.Contains(err.Error(), `step "bad"`) {
		t.Errorf("expected step-scoped error, got %v", err)
	}
}

func TestBuild_Categorize(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{
		{Type: "mkdir", Path: "d"},
		{Type: "copy", Source: "s", Destination: "dst"},
		{Type: "template", Source: "s", Destination: "dst.tmpl"},
		{Type: "exec", Command: "dotnet", Args: []string{"new"}},
		{Type: "message", Text: "hi"},
	}}
	p, err := Build(r, resWith("p", nil), conditions.Context{}, "opencode")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(p.Directories) != 1 || len(p.Files) != 2 || len(p.Commands) != 1 || len(p.Messages) != 1 {
		t.Errorf("categorize mismatch: dirs=%d files=%d cmds=%d msgs=%d",
			len(p.Directories), len(p.Files), len(p.Commands), len(p.Messages))
	}
	if p.LocalOps != 4 {
		t.Errorf("LocalOps = %d, want 4 (mkdir+2 files+dotnet exec local)", p.LocalOps)
	}
}

func TestBuild_NetworkTagging(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{
		{Type: "exec", Command: "npx", Args: []string{"impeccable"}},
		{Type: "exec", Command: "pnpm", Args: []string{"create", "next-app"}},
		{Type: "exec", Command: "dotnet", Args: []string{"new"}},
	}}
	p, err := Build(r, resWith("p", nil), conditions.Context{}, "opencode")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.NetworkOps != 2 {
		t.Errorf("NetworkOps = %d, want 2 (npx+pnpm)", p.NetworkOps)
	}
	if !p.NetworkRequired {
		t.Errorf("NetworkRequired should be true")
	}
	if p.LocalOps != 1 {
		t.Errorf("LocalOps = %d, want 1 (dotnet)", p.LocalOps)
	}
	if !p.Commands[0].Network || !p.Commands[1].Network || p.Commands[2].Network {
		t.Errorf("network flags wrong: %+v", p.Commands)
	}
}

func TestBuild_EmptySteps(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x"}
	p, err := Build(r, resWith("p", nil), conditions.Context{}, "opencode")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(p.Steps) != 0 || p.NetworkRequired {
		t.Errorf("expected empty plan, got %+v", p)
	}
}

func TestDryRun_Format(t *testing.T) {
	r := &recipe.Recipe{Version: 1, Name: "x", Steps: []recipe.Step{
		{Type: "mkdir", Path: "apps/api"},
		{Type: "template", Source: "tmpl", Destination: "docker-compose.yml"},
		{Type: "exec", Command: "dotnet", Args: []string{"new", "webapi"}},
		{Type: "message", Text: "Project created."},
	}}
	p, _ := Build(r, resWith("p", nil), conditions.Context{}, "opencode")
	out := p.DryRun()

	mustContain := []string{
		"PLAN", "Directories", "+ apps/api",
		"Files", "+ docker-compose.yml (template)",
		"Commands", "> dotnet new webapi",
		"Messages", "Project created.",
		"Local operations       3",
		"External operations    0",
		"Network required       no",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("DryRun output missing %q\n---OUTPUT---\n%s", s, out)
		}
	}
}

func TestDryRun_Empty(t *testing.T) {
	p := &Plan{}
	out := p.DryRun()
	if !strings.Contains(out, "No operations.") {
		t.Errorf("empty plan should render No operations., got %q", out)
	}
}

func TestBuild_ExternalInstallers(t *testing.T) {
	r := &recipe.Recipe{
		Version: 1, Name: "fastify-next",
		Skills: recipe.Skills{
			Bundled:  []string{"base/code-review"},
			External: []recipe.ExternalSkill{{Installer: "impeccable"}},
		},
		Agents: recipe.Agents{
			Bundled: []string{"architect"},
		},
		ExternalInstallers: map[string]recipe.ExternalInstaller{
			"impeccable": {
				Runtime: "npx",
				Package: recipe.ExternalInstallerPackage{Name: "impeccable", Version: "2.3.2"},
				Install: recipe.ExternalInstallerOp{Args: []string{"install", "--scope=project"}},
			},
		},
	}
	p, err := Build(r, resWith("p", nil), conditions.Context{}, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.BundledSkills) != 1 || len(p.BundledAgents) != 1 || len(p.ExternalInstallers) != 1 {
		t.Fatalf("skills/installers missing: bundled=%v agents=%v external=%+v", p.BundledSkills, p.BundledAgents, p.ExternalInstallers)
	}
	out := p.DryRun()
	if !strings.Contains(out, "Bundled skills") || !strings.Contains(out, "Bundled agents") || !strings.Contains(out, "External installers") {
		t.Errorf("dry-run missing skills sections:\n%s", out)
	}
	if p.NetworkOps != 1 || !p.NetworkRequired {
		t.Errorf("network tagging wrong: ops=%d required=%v", p.NetworkOps, p.NetworkRequired)
	}
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
