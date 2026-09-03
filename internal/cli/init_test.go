package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect_DotnetMonorepo(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "apps/web")
	touch(t, dir, "apps/web/package.json")
	touch(t, dir, "src/MyApp.csproj")
	touch(t, dir, "pnpm-lock.yaml")
	mkdir(t, dir, ".git")

	d := detect(dir)
	if d.Recipe != "dotnet-next" {
		t.Errorf("recipe = %q, want dotnet-next", d.Recipe)
	}
	if !d.HasGit {
		t.Errorf("git not detected")
	}
	if d.PackageManager != "pnpm" {
		t.Errorf("pkg manager = %q, want pnpm", d.PackageManager)
	}
}

func TestDetect_Rails(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Gemfile")
	d := detect(dir)
	if d.Recipe != "rails" {
		t.Errorf("recipe = %q, want rails", d.Recipe)
	}
}

func TestDetect_NodeMonorepo(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "package.json")
	mkdir(t, dir, "apps/api")
	mkdir(t, dir, "apps/web")
	touch(t, dir, "apps/api/package.json")
	touch(t, dir, "apps/web/package.json")
	d := detect(dir)
	if d.Recipe != "fastify-next" {
		t.Errorf("recipe = %q, want fastify-next", d.Recipe)
	}
}

func TestDetect_Unknown(t *testing.T) {
	dir := t.TempDir()
	d := detect(dir)
	if d.Recipe != "" {
		t.Errorf("recipe = %q, want empty", d.Recipe)
	}
}

func TestRunInit_WritesManifestWithYes(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Gemfile")

	var out bytes.Buffer
	if err := runInit(initOptions{yes: true, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if !strings.Contains(out.String(), "recipe sugerida: rails") {
		t.Errorf("output missing suggestion:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); err != nil {
		t.Errorf("manifest not written: %v", err)
	}
}

func TestRunInit_NoYesDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Gemfile")
	var out bytes.Buffer
	if err := runInit(initOptions{yes: false, out: &out, cwd: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "orotools.yaml")); err == nil {
		t.Errorf("manifest should not be written without --yes")
	}
}

func mkdir(t *testing.T, parts ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(parts...), 0o755); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, parts ...string) {
	t.Helper()
	full := filepath.Join(parts...)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
}
