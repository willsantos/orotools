package recipe

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestParse_ValidFastifyNext(t *testing.T) {
	r, err := Parse(mustReadFile(t, "testdata/valid_fastify_next.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Version != 1 {
		t.Errorf("Version = %d, want 1", r.Version)
	}
	if r.Name != "fastify-next" {
		t.Errorf("Name = %q, want fastify-next", r.Name)
	}
	// Step 0: exec pnpm create next-app apps/web
	if r.Steps[0].Type != "exec" || r.Steps[0].Command != "pnpm" {
		t.Errorf("steps[0] = %+v", r.Steps[0])
	}
	if len(r.Steps[0].Args) != 3 || r.Steps[0].Args[2] != "apps/web" {
		t.Errorf("steps[0].Args = %v", r.Steps[0].Args)
	}
	if r.Steps[0].SkipIf == nil || r.Steps[0].SkipIf.PathExists != "apps/web" {
		t.Errorf("steps[0].SkipIf = %+v", r.Steps[0].SkipIf)
	}
	// Step 1: template with when.docker==true
	if r.Steps[1].Type != "template" || r.Steps[1].Destination != "docker-compose.yml" {
		t.Errorf("steps[1] = %+v", r.Steps[1])
	}
	if r.Steps[1].When == nil || r.Steps[1].When.Variable != "docker" {
		t.Errorf("steps[1].When = %+v", r.Steps[1].When)
	}
	// Variables
	opts := r.Variables["package_manager"].Options
	if len(opts) != 3 || opts[0] != "pnpm" {
		t.Errorf("package_manager.Options = %v", opts)
	}
	// Skills
	if len(r.Skills.Bundled) != 4 {
		t.Errorf("Skills.Bundled len = %d", len(r.Skills.Bundled))
	}
	if len(r.Skills.External) != 2 || r.Skills.External[1].Installer != "tech-leads-club" {
		t.Errorf("Skills.External = %+v", r.Skills.External)
	}
	// Agents
	if len(r.Agents.Bundled) != 3 {
		t.Errorf("Agents.Bundled len = %d", len(r.Agents.Bundled))
	}
	// External installers
	inst, ok := r.ExternalInstallers["impeccable"]
	if !ok || inst.Runtime != "npx" || inst.Package.Name != "impeccable" {
		t.Errorf("external_installers[impeccable] = %+v", inst)
	}
	if len(inst.Install.Args) != 2 || inst.Install.Args[1] != "--scope=project" {
		t.Errorf("impeccable.Install.Args = %v", inst.Install.Args)
	}
}

func TestParse_ValidMinimal(t *testing.T) {
	r, err := Parse(mustReadFile(t, "testdata/valid_minimal.yaml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(r.Steps) != 1 || r.Steps[0].Type != "message" {
		t.Errorf("unexpected minimal recipe: %+v", r)
	}
}

func TestParse_InvalidFixtures(t *testing.T) {
	cases := []struct {
		path     string
		contains string // substring expected in error message
		exact    bool   // if true, must also be ErrValidation (post-decode); if false, may be decode error
	}{
		{"testdata/invalid_bad_version.yaml", "version", true},
		{"testdata/invalid_missing_name.yaml", "name", true},
		{"testdata/invalid_unknown_step.yaml", "steps[0].type", true},
		{"testdata/invalid_missing_command.yaml", "command", true},
		{"testdata/invalid_missing_path.yaml", "path", true},
		{"testdata/invalid_missing_source_dest.yaml", "destination", true},
		{"testdata/invalid_missing_text.yaml", "text", true},
		{"testdata/invalid_select_no_options.yaml", "options", true},
		{"testdata/invalid_condition_keys.yaml", "bogus_key", false},   // strict decoder
		{"testdata/invalid_unknown_field.yaml", "not_a_real_field", false}, // strict decoder
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			_, err := Parse(mustReadFile(t, c.path))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.contains) {
				t.Errorf("error %q does not contain %q", err.Error(), c.contains)
			}
			if c.exact && !errors.Is(err, ErrValidation) {
				t.Errorf("error is not ErrValidation: %v", err)
			}
		})
	}
}
