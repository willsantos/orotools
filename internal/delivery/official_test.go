package delivery

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureManifestID is the id the hermetic fixtures register. The unknown-id
// refusal uses a different id on purpose.
const fixtureManifestID = OfficialTemplateID

const fixtureTemplateYAML = `# fixture official generation template
parameters:
- name: standardsVersion
  type: string
stages: []
`

// fixtureDeliverySchemaJSON is a small but faithful subset of the official
// schemas/project-delivery.schema.json: same patterns, enums, and required
// fields that constrain the generated shape.
const fixtureDeliverySchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "application", "standards", "components"],
  "properties": {
    "schemaVersion": {"const": "1.0"},
    "application": {"$ref": "#/$defs/identifier"},
    "standards": {
      "type": "object",
      "additionalProperties": false,
      "required": ["version"],
      "properties": {
        "version": {"type": "string", "pattern": "^[0-9]+\\.[0-9]+\\.[0-9]+(?:-(?:rc|alpha|beta)\\.[1-9][0-9]*)?$"}
      }
    },
    "ci": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "provider": {"type": "string", "enum": ["azure-devops", "github-actions", "gitlab-ci", "other"]}
      }
    },
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
        "build": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "command": {"type": "string", "minLength": 1, "maxLength": 1000}
          }
        },
        "deployment": {
          "type": "object",
          "additionalProperties": false,
          "required": ["provider", "environments"],
          "properties": {
            "provider": {"type": "string", "enum": ["docker", "cloudflare-pages", "cloudflare-workers", "vercel", "static", "other"]},
            "promotionMode": {"type": "string", "enum": ["same-artifact", "native-version-activation", "reupload-same-files", "platform-rebuild"]},
            "environments": {
              "type": "object",
              "additionalProperties": {"$ref": "#/$defs/target"},
              "required": ["dev", "prd"],
              "properties": {
                "dev": {"$ref": "#/$defs/target"},
                "prd": {"$ref": "#/$defs/target"},
                "stg": {"$ref": "#/$defs/target"}
              }
            }
          }
        }
      }
    }
  }
}
`

// writeFixtureStandards writes a minimal standards checkout (manifest,
// official template, config schema), applies mutate for case-specific edits,
// commits everything and returns the fixture root and HEAD revision.
func writeFixtureStandards(t *testing.T, mutate func(root string)) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"standards/manifest.json": `{
  "distributionVersion": "0.0.0-rc.1",
  "normativeStandard": "ORO-SD-001",
  "normativeStandardVersion": "0.1.0",
  "releaseSchemaVersion": "1.0",
  "deploymentSchemaVersion": "1.0",
  "projectConfigSchemaVersion": "1.0",
  "recommendedAzureReposRef": "refs/tags/v0.0.0-rc.1",
  "generationTemplates": [
    {
      "id": "` + fixtureManifestID + `",
      "path": "pipelines/azure-devops/templates/oroborus-delivery.yml",
      "configSchema": "schemas/project-delivery.schema.json",
      "description": "fixture official generation template"
    }
  ]
}`,
		"pipelines/azure-devops/templates/oroborus-delivery.yml": fixtureTemplateYAML,
		"schemas/project-delivery.schema.json":                   fixtureDeliverySchemaJSON,
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
	if mutate != nil {
		mutate(root)
	}
	return root, commitFixtureCheckout(t, root)
}

func commitFixtureCheckout(t *testing.T, root string) string {
	t.Helper()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{
			"-C", root,
			"-c", "init.defaultBranch=main",
			"-c", "user.name=fixture",
			"-c", "user.email=fixture@example.invalid",
			"-c", "commit.gpgsign=false",
		}, args...)...)
		cmd.Env = fixtureGitEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	run("init")
	run("add", "-A")
	run("commit", "--quiet", "-m", "fixture")
	return strings.TrimSpace(run("rev-parse", "HEAD"))
}

// fixtureGitEnv strips ambient Git configuration so fixtures are hermetic.
func fixtureGitEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=",
		"GIT_CONFIG_COUNT=0",
		"HOME=/nonexistent",
		"XDG_CONFIG_HOME=/nonexistent",
	}
}

func TestLoadOfficialSourceSnapshot(t *testing.T) {
	root, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(root, revision)
	if err != nil {
		t.Fatal(err)
	}
	if source.Revision != revision || len(source.Revision) != 40 {
		t.Fatalf("revision = %q, want verified fixture HEAD %q", source.Revision, revision)
	}
	if source.DistributionVersion != "0.0.0-rc.1" {
		t.Fatalf("distributionVersion = %q", source.DistributionVersion)
	}
	if source.TemplateID != fixtureManifestID {
		t.Fatalf("template id = %q", source.TemplateID)
	}
	if source.TemplatePath != "pipelines/azure-devops/templates/oroborus-delivery.yml" {
		t.Fatalf("template path = %q", source.TemplatePath)
	}
	if string(source.TemplateBytes) != fixtureTemplateYAML {
		t.Fatal("template bytes differ from fixture")
	}
	if source.ConfigSchemaPath != "schemas/project-delivery.schema.json" || len(source.SchemaBytes) == 0 {
		t.Fatalf("schema snapshot = %q, %d bytes", source.ConfigSchemaPath, len(source.SchemaBytes))
	}
}

func TestLoadOfficialSourceRefusals(t *testing.T) {
	forty := strings.Repeat("a", 40)
	cases := []struct {
		name   string
		mutate func(root string)
		rev    string
	}{
		{"unverified-revision", nil, "deadbeef"},
		{"empty-revision", nil, ""},
		{"missing-manifest", func(root string) {
			if err := os.Remove(filepath.Join(root, "standards", "manifest.json")); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"malformed-manifest", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"missing-distribution-version", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			content := strings.ReplaceAll(mustRead(t, path), `"distributionVersion": "0.0.0-rc.1",`, "")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"malformed-distribution-version", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			content := strings.Replace(mustRead(t, path), "0.0.0-rc.1", "0.0.0-test", 1)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"no-registry", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			if err := os.WriteFile(path, []byte(`{"distributionVersion": "0.0.0-rc.1"}`), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"unknown-template-id", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			if err := os.WriteFile(path, []byte(strings.Replace(mustRead(t, path), fixtureManifestID, "oroborus-delivery-test", 1)), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"duplicate-template-id", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			duplicated := strings.Replace(mustRead(t, path),
				`      "description": "fixture official generation template"
    }`,
				`      "description": "fixture official generation template"
    },
    {
      "id": "`+fixtureManifestID+`",
      "path": "pipelines/azure-devops/templates/oroborus-delivery.yml",
      "configSchema": "schemas/project-delivery.schema.json",
      "description": "duplicate entry"
    }`, 1)
			if err := os.WriteFile(path, []byte(duplicated), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"escaping-template-path", func(root string) {
			path := filepath.Join(root, "standards", "manifest.json")
			if err := os.WriteFile(path, []byte(strings.Replace(mustRead(t, path), "pipelines/azure-devops/templates/oroborus-delivery.yml", "../../etc/evil.yml", 1)), 0644); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"missing-template-file", func(root string) {
			if err := os.Remove(filepath.Join(root, "pipelines", "azure-devops", "templates", "oroborus-delivery.yml")); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"symlinked-template-file", func(root string) {
			templatePath := filepath.Join(root, "pipelines", "azure-devops", "templates", "oroborus-delivery.yml")
			outside := filepath.Join(t.TempDir(), "evil.yml")
			if err := os.WriteFile(outside, []byte("evil: true"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(templatePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, templatePath); err != nil {
				t.Fatal(err)
			}
		}, forty},
		{"missing-schema-file", func(root string) {
			if err := os.Remove(filepath.Join(root, "schemas", "project-delivery.schema.json")); err != nil {
				t.Fatal(err)
			}
		}, forty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := writeFixtureStandards(t, tc.mutate)
			source, err := LoadOfficialSource(root, tc.rev)
			if err == nil {
				t.Fatal("expected refusal")
			}
			if !errors.Is(err, ErrOfficialSourceUnavailable) {
				t.Fatalf("error is not ErrOfficialSourceUnavailable: %v", err)
			}
			if source != nil {
				t.Fatal("snapshot returned on refusal")
			}
		})
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReadVerifiedFileRefusesIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deep", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deep", "dir", "file.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := readVerifiedFile(root, "deep/dir/file.txt"); err != nil || string(data) != "inside" {
		t.Fatalf("happy path failed: %v, %q", err, data)
	}

	// Replace the intermediate directory with a symlink to an outside
	// directory carrying identical content: the read must still refuse.
	if err := os.MkdirAll(filepath.Join(outside, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "dir", "file.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "deep")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "deep")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := readVerifiedFile(root, "deep/dir/file.txt")
	if err == nil {
		t.Fatal("intermediate symlinked directory was followed")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unexpected refusal: %v", err)
	}

	// Intermediate component that is a regular file must also be refused.
	if err := os.RemoveAll(filepath.Join(root, "deep")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deep"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readVerifiedFile(root, "deep/dir/file.txt"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected intermediate non-directory refusal, got %v", err)
	}

	// Symlink on the final component stays refused.
	if err := os.Symlink(filepath.Join(outside, "dir", "file.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := readVerifiedFile(root, "link.txt"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected final-symlink refusal, got %v", err)
	}
}

func TestLoadOfficialSourceRefusesIntermediateSymlink(t *testing.T) {
	root, revision := writeFixtureStandards(t, nil)
	schema, err := os.ReadFile(filepath.Join(root, "schemas", "project-delivery.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "project-delivery.schema.json"), schema, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "schemas")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "schemas")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source, err := LoadOfficialSource(root, revision)
	if err == nil {
		t.Fatal("intermediate symlinked schemas/ directory was followed")
	}
	if !errors.Is(err, ErrOfficialSourceUnavailable) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if source != nil {
		t.Fatal("snapshot returned on refusal")
	}
}
