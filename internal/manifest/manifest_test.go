package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)

	m := &Manifest{
		Version: 1,
		Recipe:  RecipeRef{Name: "fastify-next", Version: 1},
		Project: ProjectRef{Name: "flagview"},
		Variables: map[string]any{
			"database":        "postgres",
			"package_manager": "pnpm",
			"docker":          true,
		},
		Repository: ProviderRef{Provider: "azure-devops"},
		Pipeline:   ProviderRef{Provider: "azure-pipelines"},
		AI:         AIRef{Agent: "opencode"},
		Skills:     SkillsRef{Bundled: []string{"code-review"}, External: []string{"impeccable"}},
		Agents:     AgentsRef{Bundled: []string{"architect"}},
	}
	if err := Write(path, m); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Version != 1 || got.Project.Name != "flagview" || got.Recipe.Name != "fastify-next" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Variables["database"] != "postgres" {
		t.Errorf("variable lost: %+v", got.Variables)
	}
	if got.Repository.Provider != "azure-devops" || got.Pipeline.Provider != "azure-pipelines" {
		t.Errorf("providers lost: %+v", got)
	}
	if len(got.Skills.Bundled) != 1 || got.Skills.Bundled[0] != "code-review" {
		t.Errorf("skills lost: %+v", got.Skills)
	}
}

func TestWrite_AddsHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", Filename)
	if err := Write(path, &Manifest{Version: 1, Project: ProjectRef{Name: "x"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# orotools.yaml") {
		t.Errorf("missing header: %s", b)
	}
}

func TestRead_Missing(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "read manifest") {
		t.Errorf("expected read error, got %v", err)
	}
}

func TestParse_StrictAndVersion(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"bad version", "version: 2\nproject:\n    name: x\n", "version"},
		{"missing project name", "version: 1\n", "project.name"},
		{"unknown field", "version: 1\nproject:\n    name: x\nbogus: y\n", "parse manifest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v does not contain %q", err, c.want)
			}
		})
	}
}

func TestDefault(t *testing.T) {
	m := Default()
	if m.Version != 1 {
		t.Errorf("Default version = %d", m.Version)
	}
}
