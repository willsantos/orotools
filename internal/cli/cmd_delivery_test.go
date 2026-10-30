package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeDeliveryStandardsFixture builds a minimal clean standards checkout (the
// same shape internal/delivery fixtures use) and returns root + HEAD revision.
func writeDeliveryStandardsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"standards/manifest.json": `{
  "distributionVersion": "0.0.0-rc.1",
  "generationTemplates": [
    {
      "id": "oroborus-delivery",
      "path": "pipelines/azure-devops/templates/oroborus-delivery.yml",
      "configSchema": "schemas/project-delivery.schema.json",
      "description": "fixture"
    }
  ]
}`,
		"pipelines/azure-devops/templates/oroborus-delivery.yml": "parameters: []\n",
		"schemas/project-delivery.schema.json":                   deliveryTestSchemaJSON,
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixtureCheckout(t, root)
	return root
}

// runDeliveryGenerate executes `oro delivery generate` with the given extra
// args, returning combined output and the command error.
func execDeliveryGenerate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"delivery", "generate"}, args...))
	err := root.Execute()
	return out.String(), err
}

func deliveryGenerateArgs(project, standardsRoot string) []string {
	return []string{
		"--application", "sample-app",
		"--project", project,
		"--standards-root", standardsRoot,
		"--deploy-provider", "static",
		"--promotion-mode", "reupload-same-files",
		"--env-dev", "sample-app-dev",
		"--env-prd", "sample-app-prd",
	}
}

func TestDeliveryGeneratePreviewWritesNothing(t *testing.T) {
	standardsRoot := writeDeliveryStandardsFixture(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := deliveryTreeSnapshot(t, project)
	out, err := execDeliveryGenerate(t, deliveryGenerateArgs(project, standardsRoot)...)
	if err != nil {
		t.Fatalf("preview failed: %v", out)
	}
	for _, want := range []string{"stack detectada: go", "go build ./...", "distributionVersion: 0.0.0-rc.1", "validação do schema oficial: OK", "conflitos: nenhum", "nada foi escrito"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview output missing %q:\n%s", want, out)
		}
	}
	if got := deliveryTreeSnapshot(t, project); !mapsEqualString(got, before) {
		t.Fatalf("preview wrote files: before %v after %v", before, got)
	}
}

func TestDeliveryGenerateWriteCreatesBothFiles(t *testing.T) {
	standardsRoot := writeDeliveryStandardsFixture(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args := append(deliveryGenerateArgs(project, standardsRoot), "--write")
	if out, err := execDeliveryGenerate(t, args...); err != nil {
		t.Fatalf("write failed: %v: %s", err, out)
	}
	config, err := os.ReadFile(filepath.Join(project, ".oroborus", "delivery.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "version: 0.0.0-rc.1") || !strings.Contains(string(config), "command: go build ./...") {
		t.Fatalf("unexpected delivery.yaml:\n%s", config)
	}
	pipeline, err := os.ReadFile(filepath.Join(project, "azure-pipelines.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pipeline), "ref: refs/tags/v0.0.0-rc.1") || !strings.Contains(string(pipeline), "oroborus-delivery.yml@standards") {
		t.Fatalf("unexpected azure-pipelines.yml:\n%s", pipeline)
	}
	// Re-run with --write: exclusive creation refuses a second write.
	if _, err := execDeliveryGenerate(t, args...); err == nil {
		t.Fatal("second write was allowed to overwrite")
	}
}

func TestDeliveryGenerateRequiresStandardsRoot(t *testing.T) {
	project := t.TempDir()
	out, err := execDeliveryGenerate(t, "--application", "sample-app", "--project", project, "--deploy-provider", "static", "--promotion-mode", "reupload-same-files", "--env-dev", "d", "--env-prd", "p")
	if err == nil {
		t.Fatal("missing --standards-root accepted")
	}
	if !strings.Contains(err.Error(), "verified official standards source is required") || !strings.Contains(err.Error(), "--standards-root") {
		t.Fatalf("fail-closed message not actionable: %v", err)
	}
	if !strings.Contains(out, "") {
		t.Fatal("unexpected output")
	}
}

func TestDeliveryGenerateRefusesDirtyStandardsCheckout(t *testing.T) {
	cases := map[string]func(root string){
		"modified-tracked-file": func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(content, []byte("\n")...), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"deleted-tracked-file": func(root string) {
			if err := os.Remove(filepath.Join(root, "schemas", "project-delivery.schema.json")); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked-tracked-file": func(root string) {
			templatePath := filepath.Join(root, "pipelines", "azure-devops", "templates", "oroborus-delivery.yml")
			outside := filepath.Join(t.TempDir(), "evil.yml")
			if err := os.WriteFile(outside, []byte("evil: true"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(templatePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, templatePath); err != nil {
				t.Fatal(err)
			}
		},
		"not-a-checkout": func(root string) {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, dirty := range cases {
		t.Run(name, func(t *testing.T) {
			standardsRoot := writeDeliveryStandardsFixture(t)
			dirty(standardsRoot)
			project := t.TempDir()
			_, err := execDeliveryGenerate(t, deliveryGenerateArgs(project, standardsRoot)...)
			if err == nil {
				t.Fatal("dirty standards checkout accepted")
			}
			if !strings.Contains(err.Error(), "standards checkout verification failed") {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

// Untracked/gitignored artifacts of a developer checkout are never part of the
// verified (tracked) set and are never read or followed, so they do not block
// generation.
func TestDeliveryGenerateToleratesUntrackedArtifacts(t *testing.T) {
	standardsRoot := writeDeliveryStandardsFixture(t)
	if err := os.MkdirAll(filepath.Join(standardsRoot, ".venv", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent/python", filepath.Join(standardsRoot, ".venv", "bin", "python")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(standardsRoot, "STRAY.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if out, err := execDeliveryGenerate(t, deliveryGenerateArgs(project, standardsRoot)...); err != nil {
		t.Fatalf("untracked artifacts should not block generation: %v: %s", err, out)
	}
}

func TestDeliveryGenerateConflictReportsAndWritesNothing(t *testing.T) {
	standardsRoot := writeDeliveryStandardsFixture(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "azure-pipelines.yml"), []byte("custom: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := deliveryTreeSnapshot(t, project)
	args := append(deliveryGenerateArgs(project, standardsRoot), "--write")
	out, err := execDeliveryGenerate(t, args...)
	if err == nil {
		t.Fatalf("conflicting write accepted: %s", out)
	}
	if !strings.Contains(err.Error(), "azure-pipelines.yml") || !strings.Contains(err.Error(), "refusing to write") {
		t.Fatalf("conflict error not actionable: %v", err)
	}
	if !strings.Contains(out, "azure-pipelines.yml já existe") {
		t.Fatalf("preview did not report the conflict:\n%s", out)
	}
	if got := deliveryTreeSnapshot(t, project); !mapsEqualString(got, before) {
		t.Fatalf("conflicting write touched disk: %v", got)
	}
}

func deliveryTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		snapshot[rel] = info.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func mapsEqualString(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// commitFixtureCheckout commits the fixture with a hermetic git environment.
func commitFixtureCheckout(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{
			"-C", root,
			"-c", "init.defaultBranch=main",
			"-c", "user.name=fixture",
			"-c", "user.email=fixture@example.invalid",
			"-c", "commit.gpgsign=false",
		}, args...)...)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_TERMINAL_PROMPT=0",
			"GIT_ALLOW_PROTOCOL=",
			"HOME=/nonexistent",
			"XDG_CONFIG_HOME=/nonexistent",
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("add", "-A")
	run("commit", "--quiet", "-m", "fixture")
}

// deliveryTestSchemaJSON mirrors the fixture schema shape used by the
// internal/delivery tests (identifier/enum/required contract).
const deliveryTestSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "application", "standards", "components"],
  "properties": {
    "schemaVersion": {"const": "1.0"},
    "application": {"$ref": "#/$defs/identifier"},
    "standards": {"type": "object", "additionalProperties": false, "required": ["version"], "properties": {"version": {"type": "string", "pattern": "^[0-9]+\\.[0-9]+\\.[0-9]+(?:-(?:rc|alpha|beta)\\.[1-9][0-9]*)?$"}}},
    "ci": {"type": "object", "additionalProperties": false, "properties": {"provider": {"type": "string", "enum": ["azure-devops", "github-actions", "gitlab-ci", "other"]}}},
    "components": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/component"}}
  },
  "$defs": {
    "identifier": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{1,62}$"},
    "relativePath": {"type": "string", "minLength": 1, "maxLength": 512},
    "target": {"type": "string", "minLength": 1, "maxLength": 200, "pattern": "^[^\\r\\n]+$"},
    "component": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "path", "deployment"],
      "properties": {
        "name": {"$ref": "#/$defs/identifier"},
        "path": {"$ref": "#/$defs/relativePath"},
        "build": {"type": "object", "additionalProperties": false, "properties": {"command": {"type": "string", "minLength": 1, "maxLength": 1000}}},
        "deployment": {
          "type": "object",
          "additionalProperties": false,
          "required": ["provider", "environments"],
          "properties": {
            "provider": {"type": "string", "enum": ["docker", "cloudflare-pages", "cloudflare-workers", "vercel", "static", "other"]},
            "promotionMode": {"type": "string", "enum": ["same-artifact", "native-version-activation", "reupload-same-files", "platform-rebuild"]},
            "environments": {"type": "object", "additionalProperties": {"$ref": "#/$defs/target"}, "required": ["dev", "prd"], "properties": {"dev": {"$ref": "#/$defs/target"}, "prd": {"$ref": "#/$defs/target"}, "stg": {"$ref": "#/$defs/target"}}}
          }
        }
      }
    }
  }
}
`

func TestDeliveryGenerateRefusesIntermediateSymlinkInStandardsCheckout(t *testing.T) {
	standardsRoot := writeDeliveryStandardsFixture(t)
	schema, err := os.ReadFile(filepath.Join(standardsRoot, "schemas", "project-delivery.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "project-delivery.schema.json"), schema, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(standardsRoot, "schemas")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(standardsRoot, "schemas")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	project := t.TempDir()
	before := deliveryTreeSnapshot(t, project)
	out, err := execDeliveryGenerate(t, deliveryGenerateArgs(project, standardsRoot)...)
	if err == nil {
		t.Fatalf("intermediate symlinked standards directory was accepted:\n%s", out)
	}
	if !strings.Contains(out+err.Error(), "symlink") {
		t.Fatalf("refusal does not mention symlink: %v: %s", err, out)
	}
	if got := deliveryTreeSnapshot(t, project); !mapsEqualString(got, before) {
		t.Fatalf("project was written despite refusal: %v", got)
	}
}
