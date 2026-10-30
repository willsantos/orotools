package delivery

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectStacks(t *testing.T) {
	for file, want := range map[string]string{"go.mod": "go", "App.csproj": "dotnet", "pyproject.toml": "python", "composer.json": "php", "yarn.lock": "node"} {
		t.Run(want, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, file), []byte("evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := DetectStack(root)
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}
func TestDetectRejectsInvalidEvidence(t *testing.T) {
	for _, kind := range []string{"directory", "dangling-symlink", "external-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "go.mod")
			switch kind {
			case "directory":
				_ = os.Mkdir(p, 0700)
			case "dangling-symlink":
				_ = os.Symlink(filepath.Join(root, "missing"), p)
			case "external-symlink":
				external := filepath.Join(t.TempDir(), "go.mod")
				_ = os.WriteFile(external, []byte("module x"), 0600)
				_ = os.Symlink(external, p)
			}
			if _, err := DetectStack(root); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
func TestDetectUsesLiteralRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project[1]")
	_ = os.Mkdir(root, 0700)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x"), 0600)
	if got, err := DetectStack(root); err != nil || got != "go" {
		t.Fatalf("got %q, %v", got, err)
	}
}
func TestDetectMixedEvidenceIsUnclassified(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"go.mod", "package.json"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := DetectStack(root)
	if err == nil {
		t.Fatalf("mixed evidence classified as %q", stack)
	}
	if !strings.Contains(err.Error(), "mixed project evidence") {
		t.Fatalf("error = %v, want mixed evidence refusal", err)
	}
}

// planOptions returns complete explicit options for an application identifier.
func planOptions(application string) Options {
	return Options{
		Application:    application,
		Provider:       "azure-devops",
		DeployProvider: "static",
		PromotionMode:  "reupload-same-files",
		EnvDev:         application + "-dev",
		EnvPrd:         application + "-prd",
	}
}

// writeStackProject writes stack evidence files under a fresh project root.
func writeStackProject(t *testing.T, evidence ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, file := range evidence {
		if err := os.WriteFile(filepath.Join(root, file), []byte("evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
		snapshot[rel] = entry.Type().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	if got := snapshotTree(t, root); !equalSnapshots(before, got) {
		t.Fatalf("filesystem changed: before %v, after %v", before, got)
	}
}

func equalSnapshots(a, b map[string]string) bool {
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

func TestPlanGenerationStackMatrix(t *testing.T) {
	cases := []struct {
		name            string
		evidence        []string
		wantStack       string
		wantCommand     string
		wantBuildInYAML bool
	}{
		{"go", []string{"go.mod"}, "go", "go build ./...", true},
		{"dotnet", []string{"App.csproj"}, "dotnet", "dotnet publish -c Release", true},
		{"python", []string{"pyproject.toml"}, "python", "python -m build", true},
		{"php", []string{"composer.json"}, "php", "composer install --no-dev", true},
		{"node", []string{"package.json"}, "node", "npm ci && npm run build", true},
		{"none", nil, "", "", false},
		{"mixed", []string{"go.mod", "package.json"}, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			standardsRoot, revision := writeFixtureStandards(t, nil)
			source, err := LoadOfficialSource(standardsRoot, revision)
			if err != nil {
				t.Fatal(err)
			}
			project := writeStackProject(t, tc.evidence...)
			plan, err := PlanGeneration(project, source, planOptions("sample-app"))
			if err != nil {
				t.Fatal(err)
			}
			if plan.Stack != tc.wantStack {
				t.Fatalf("stack = %q, want %q", plan.Stack, tc.wantStack)
			}
			if plan.BuildCommand != tc.wantCommand {
				t.Fatalf("build command = %q, want %q", plan.BuildCommand, tc.wantCommand)
			}
			hasBuild := strings.Contains(string(plan.DeliveryYAML), "command:")
			if hasBuild != tc.wantBuildInYAML {
				t.Fatalf("delivery.yaml contains build.command = %v, want %v\n%s", hasBuild, tc.wantBuildInYAML, plan.DeliveryYAML)
			}
			if tc.wantStack == "" && plan.StackNote == "" {
				t.Fatal("unclassified stack without explanatory note")
			}
			// Pin contract appears in both generated files.
			for _, artifact := range []string{string(plan.DeliveryYAML), string(plan.PipelineYAML)} {
				if !strings.Contains(artifact, "0.0.0-rc.1") {
					t.Fatalf("artifact missing pinned distributionVersion:\n%s", artifact)
				}
			}
			pipeline := string(plan.PipelineYAML)
			if !strings.Contains(pipeline, "ref: refs/tags/v0.0.0-rc.1") ||
				!strings.Contains(pipeline, "repository: standards") ||
				!strings.Contains(pipeline, fixtureManifestID+".yml@standards") ||
				!strings.Contains(pipeline, "standardsVersion: '0.0.0-rc.1'") {
				t.Fatalf("pipeline does not mirror the official pin example:\n%s", pipeline)
			}
			if !strings.Contains(string(plan.DeliveryYAML), "standards:\n    version: 0.0.0-rc.1") {
				t.Fatalf("delivery.yaml missing pinned standards version:\n%s", plan.DeliveryYAML)
			}
		})
	}
}

func TestPlanPreviewWritesNothingAndIsIdempotent(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	project := writeStackProject(t, "go.mod")
	before := snapshotTree(t, project)
	first, err := PlanGeneration(project, source, planOptions("sample-app"))
	if err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, project, before)
	second, err := PlanGeneration(project, source, planOptions("sample-app"))
	if err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, project, before)
	if string(first.DeliveryYAML) != string(second.DeliveryYAML) || string(first.PipelineYAML) != string(second.PipelineYAML) {
		t.Fatal("re-preview is not byte-identical")
	}
}

func TestPlanWriteCreatesExactlyTheTwoFiles(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	project := writeStackProject(t, "go.mod")
	plan, err := PlanGeneration(project, source, planOptions("sample-app"))
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(project); err != nil {
		t.Fatal(err)
	}
	entries := snapshotTree(t, project)
	want := map[string]string{
		"go.mod":                  "",
		".oroborus":               "",
		".oroborus/delivery.yaml": "",
		"azure-pipelines.yml":     "",
	}
	for path := range want {
		if _, ok := entries[path]; !ok {
			t.Fatalf("missing %s after write; tree = %v", path, entries)
		}
	}
	if len(entries) != len(want) {
		t.Fatalf("unexpected extra entries: %v", entries)
	}
	config, err := os.ReadFile(filepath.Join(project, ".oroborus", "delivery.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != string(plan.DeliveryYAML) {
		t.Fatal("written config differs from plan")
	}
	pipeline, err := os.ReadFile(filepath.Join(project, "azure-pipelines.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(pipeline) != string(plan.PipelineYAML) {
		t.Fatal("written pipeline differs from plan")
	}
}

func TestPlanWriteRefusesConflictsWithZeroWrites(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []string{filepath.Join("azure-pipelines.yml"), filepath.Join(".oroborus", "delivery.yaml")} {
		project := writeStackProject(t, "go.mod")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(project, conflict)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, conflict), []byte("custom CI"), 0600); err != nil {
			t.Fatal(err)
		}
		before := snapshotTree(t, project)
		plan, err := PlanGeneration(project, source, planOptions("sample-app"))
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Conflicts) == 0 {
			t.Fatal("conflict not detected in plan")
		}
		if err := plan.Write(project); err == nil {
			t.Fatal("write accepted conflict")
		} else if !strings.Contains(err.Error(), "refusing to write") {
			t.Fatalf("conflict error = %v", err)
		}
		assertTreeUnchanged(t, project, before)
	}
}

func TestPlanSchemaInvalidFailsClosedBeforeWrite(t *testing.T) {
	for name, schema := range map[string]string{
		"unacceptable-shape": `{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type": "object",
			"additionalProperties": false,
			"required": ["schemaVersion", "application", "standards", "components", "approvedBy"],
			"properties": {"schemaVersion": {"const": "1.0"}, "application": {}, "standards": {}, "components": {}, "approvedBy": {"const": "governance"}}
		}`,
		"malformed-json": `{not json`,
	} {
		t.Run(name, func(t *testing.T) {
			standardsRoot, revision := writeFixtureStandards(t, func(root string) {
				path := filepath.Join(root, "schemas", "project-delivery.schema.json")
				if err := os.WriteFile(path, []byte(schema), 0644); err != nil {
					t.Fatal(err)
				}
			})
			source, err := LoadOfficialSource(standardsRoot, revision)
			if err != nil {
				t.Fatal(err)
			}
			project := writeStackProject(t, "go.mod")
			before := snapshotTree(t, project)
			if _, err := PlanGeneration(project, source, planOptions("sample-app")); err == nil {
				t.Fatal("schema-invalid generation accepted")
			}
			assertTreeUnchanged(t, project, before)
		})
	}
}

func TestPlanRefusesInvalidEnumData(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	project := writeStackProject(t, "go.mod")
	opts := planOptions("sample-app")
	opts.DeployProvider = "hostinger"
	if _, err := PlanGeneration(project, source, opts); err == nil || !strings.Contains(err.Error(), "official config schema") {
		t.Fatalf("expected schema refusal for invented provider, got %v", err)
	}
	opts = planOptions("sample-app")
	opts.Provider = "hostinger"
	if _, err := PlanGeneration(project, source, opts); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("expected explicit unsupported destination, got %v", err)
	}
}

func TestPlanAsksForMissingExplicitData(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	project := writeStackProject(t, "go.mod")
	before := snapshotTree(t, project)
	opts := planOptions("sample-app")
	opts.DeployProvider = ""
	opts.EnvPrd = ""
	_, err = PlanGeneration(project, source, opts)
	if err == nil {
		t.Fatal("missing explicit data accepted")
	}
	for _, flag := range []string{"--deploy-provider", "--env-prd"} {
		if !strings.Contains(err.Error(), flag) {
			t.Fatalf("error does not ask for %s: %v", flag, err)
		}
	}
	assertTreeUnchanged(t, project, before)
}

func TestPlanBuildCommandOverride(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	// Override on a classified stack replaces the stack-derived command.
	project := writeStackProject(t, "go.mod")
	opts := planOptions("sample-app")
	opts.BuildCommand = "make release"
	plan, err := PlanGeneration(project, source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.BuildCommand != "make release" || plan.BuildCommandSource != "flag" {
		t.Fatalf("override not respected: %q/%q", plan.BuildCommand, plan.BuildCommandSource)
	}
	// Override supplies the command for an unclassified stack.
	empty := writeStackProject(t)
	opts = planOptions("sample-app")
	opts.BuildCommand = "make release"
	plan, err = PlanGeneration(empty, source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.DeliveryYAML), "command: make release") {
		t.Fatalf("override missing from delivery.yaml:\n%s", plan.DeliveryYAML)
	}
}

func TestPlanValidatesApplicationIdentifier(t *testing.T) {
	standardsRoot, revision := writeFixtureStandards(t, nil)
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatal(err)
	}
	project := writeStackProject(t, "go.mod")
	for _, bad := range []string{"", "Sample", "tem_espaço", "-lead", "a"} {
		opts := planOptions(bad)
		if _, err := PlanGeneration(project, source, opts); err == nil || !strings.Contains(err.Error(), "--application") {
			t.Fatalf("application %q accepted: %v", bad, err)
		}
	}
}

func TestPlanFailsClosedWithoutVerifiedSource(t *testing.T) {
	project := writeStackProject(t, "go.mod")
	_, err := PlanGeneration(project, nil, planOptions("sample-app"))
	if err == nil || !strings.Contains(err.Error(), "verified official standards source is required") {
		t.Fatalf("expected fail-closed posture, got %v", err)
	}
}

func TestWritePreviewRejectsSymlinkParent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".oroborus")); err != nil {
		t.Fatal(err)
	}
	if err := WritePreview(root, []byte("config"), []byte("pipeline")); err == nil {
		t.Fatal("accepted symlink parent")
	}
	if _, err := os.Lstat(filepath.Join(outside, "delivery.yaml")); !os.IsNotExist(err) {
		t.Fatal("wrote outside project root")
	}
}
func TestWritePreviewConflictAndExclusiveCreation(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "azure-pipelines.yml")
	_ = os.WriteFile(existing, []byte("custom"), 0600)
	if err := WritePreview(root, []byte("config"), []byte("generated")); err == nil {
		t.Fatal("overwrote CI")
	}
	if _, err := os.Lstat(filepath.Join(root, ".oroborus", "delivery.yaml")); !os.IsNotExist(err) {
		t.Fatal("left partial config")
	}
	root = t.TempDir()
	if err := WritePreview(root, []byte("config"), []byte("pipeline")); err != nil {
		t.Fatal(err)
	}
	if err := WritePreview(root, []byte("other"), []byte("other")); err == nil {
		t.Fatal("exclusive writer allowed overwrite")
	}
}

func TestWritePreviewRollsBackOnlyItsFilesOnLaterFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "azure-pipelines.yml"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WritePreview(root, []byte("new config"), []byte("pipeline")); err == nil {
		t.Fatal("expected second-target failure")
	}
	if _, err := os.Lstat(filepath.Join(root, ".oroborus", "delivery.yaml")); !os.IsNotExist(err) {
		t.Fatal("did not roll back created config")
	}
}

func TestRollbackPreservesReplacedEntries(t *testing.T) {
	for _, kind := range []string{"regular", "symlink-to-renamed-original"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			f, err := root.OpenFile("created", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			info := fileInfo(f)
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dir, "created"), filepath.Join(dir, "saved")); err != nil {
				t.Fatal(err)
			}
			if kind == "regular" {
				if err := os.WriteFile(filepath.Join(dir, "created"), []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink("saved", filepath.Join(dir, "created")); err != nil {
				t.Fatal(err)
			}
			rollbackCreatedRoot(root, []createdFile{{"created", info}})
			if _, err := os.Lstat(filepath.Join(dir, "created")); err != nil {
				t.Fatalf("removed replacement: %v", err)
			}
		})
	}
}
