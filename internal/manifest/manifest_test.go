package manifest

import (
	"bytes"
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
		{"bad version", "version: 3\nproject:\n    name: x\n", "version"},
		{"missing project name", "version: 1\n", "project.name"},
		{"unknown field", "version: 1\nproject:\n    name: x\nbogus: y\n", "parse manifest"},
		{"unknown field v2", "version: 2\nproject:\n    name: x\nbogus: y\n", "parse manifest"},
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

func TestParse_V2WithManaged(t *testing.T) {
	src := `version: 2
project:
    name: x
skills:
    bundled:
        - base/code-review
    managed:
        - source: github:willsantos/skills_AI
          name: local-pr-review
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := ManagedSkillRef{Source: "github:willsantos/skills_AI", Name: "local-pr-review"}
	if len(m.Skills.Managed) != 1 || m.Skills.Managed[0] != want {
		t.Errorf("Managed = %+v, want [%+v]", m.Skills.Managed, want)
	}
}

func TestParse_V2ManagedInvalid(t *testing.T) {
	cases := map[string]string{
		"missing name":     "version: 2\nproject:\n    name: x\nskills:\n    managed:\n        - source: github:willsantos/skills_AI\n",
		"missing source":   "version: 2\nproject:\n    name: x\nskills:\n    managed:\n        - name: s\n",
		"duplicated entry": "version: 2\nproject:\n    name: x\nskills:\n    managed:\n        - source: gh\n          name: s\n        - source: gh\n          name: s\n",
	}
	for label, src := range cases {
		t.Run(label, func(t *testing.T) {
			if _, err := Parse([]byte(src)); err == nil {
				t.Fatalf("expected error for %s", label)
			}
		})
	}
}

func TestWriteReadRoundTripV2ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	m := &Manifest{
		Version: 2,
		Project: ProjectRef{Name: "x"},
		Skills: SkillsRef{
			Bundled: []string{"base/code-review"},
			Managed: []ManagedSkillRef{{Source: "github:willsantos/skills_AI", Name: "local-pr-review"}},
		},
	}
	if err := Write(path, m); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(path, reloaded); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Errorf("round-trip not byte-identical:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteV1HasNoManagedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := Write(path, &Manifest{Version: 1, Project: ProjectRef{Name: "x"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "managed:") {
		t.Errorf("v1 manifest should not carry a managed key:\n%s", b)
	}
}

func TestAddManagedSkillDedupesAndPromotes(t *testing.T) {
	m := Default() // v1
	m.AddManagedSkill("github:willsantos/skills_AI", "local-pr-review")
	if m.Version != 2 {
		t.Errorf("version = %d, want 2 after first managed ref", m.Version)
	}
	m.AddManagedSkill("github:willsantos/skills_AI", "local-pr-review")
	if len(m.Skills.Managed) != 1 {
		t.Errorf("Managed = %+v, want single deduped entry", m.Skills.Managed)
	}
	m.AddManagedSkill("github:willsantos/skills_AI", "azure-mermaid-compatible")
	if len(m.Skills.Managed) != 2 {
		t.Errorf("Managed = %+v, want 2 entries", m.Skills.Managed)
	}
}
