package release_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type goreleaserConfig struct {
	Builds []struct {
		Binary  string   `yaml:"binary"`
		Main    string   `yaml:"main"`
		Goos    []string `yaml:"goos"`
		Goarch  []string `yaml:"goarch"`
		Ldflags []string `yaml:"ldflags"`
	} `yaml:"builds"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestGoReleaserConfigPlatforms(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".goreleaser.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	if len(cfg.Builds) != 1 {
		t.Fatalf("builds = %d, want 1", len(cfg.Builds))
	}

	build := cfg.Builds[0]
	if build.Binary != "oro" {
		t.Errorf("binary = %q, want oro", build.Binary)
	}
	if build.Main != "./cmd/oro" {
		t.Errorf("main = %q, want ./cmd/oro", build.Main)
	}

	wantGOOS := []string{"linux", "darwin", "windows"}
	for _, goos := range wantGOOS {
		if !slices.Contains(build.Goos, goos) {
			t.Errorf("goos missing %q, got %v", goos, build.Goos)
		}
	}

	wantGOARCH := []string{"amd64", "arm64"}
	for _, goarch := range wantGOARCH {
		if !slices.Contains(build.Goarch, goarch) {
			t.Errorf("goarch missing %q, got %v", goarch, build.Goarch)
		}
	}

	foundVersionLDFlag := false
	for _, flag := range build.Ldflags {
		if strings.Contains(flag, "main.version=") {
			foundVersionLDFlag = true
			break
		}
	}
	if !foundVersionLDFlag {
		t.Errorf("ldflags missing main.version injection, got %v", build.Ldflags)
	}
}
