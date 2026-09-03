package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRecipeList(t *testing.T) {
	var out bytes.Buffer
	if err := runRecipeList(&out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dotnet", "dotnet-next", "rails", "fastify-next"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("list missing %q:\n%s", name, out.String())
		}
	}
}

func TestRunNew_StackDryRun(t *testing.T) {
	workDir := t.TempDir()
	restore := chdir(t, workDir)
	defer restore()

	var out bytes.Buffer
	err := runNew(newOptions{
		name:  "stack-app",
		stack: "fastify-next",
		yes:   true,
		dryRun: true,
		out:   &out,
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "PLAN") || !strings.Contains(out.String(), "apps/api") {
		t.Errorf("unexpected dry-run output:\n%s", out.String())
	}
}
