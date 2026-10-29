package delivery

import (
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
func TestGenerationFailsClosed(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x"), 0600)
	if _, _, err := Generate(root, "sample", "azure-devops"); err == nil || !strings.Contains(err.Error(), "schema validation") {
		t.Fatalf("expected fail-closed generation, got %v", err)
	}
	if _, _, err := Generate(root, "sample", "hostinger"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected explicit unsupported destination, got %v", err)
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
