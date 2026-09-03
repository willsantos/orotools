package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/manifest"
)

func writeBaseManifest(t *testing.T, dir string) {
	t.Helper()
	m := &manifest.Manifest{
		Version: 1,
		Project: manifest.ProjectRef{Name: "demo"},
	}
	if err := manifest.Write(filepath.Join(dir, manifest.Filename), m); err != nil {
		t.Fatal(err)
	}
}

func TestRunAdd_Skill(t *testing.T) {
	dir := t.TempDir()
	writeBaseManifest(t, dir)
	path := filepath.Join(dir, manifest.Filename)
	var out bytes.Buffer
	if err := runAdd(addOptions{kind: "skill", value: "code-review", manifestPath: path, out: &out}); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Read(path)
	if len(m.Skills.Bundled) != 1 || m.Skills.Bundled[0] != "code-review" {
		t.Errorf("skill not added: %+v", m.Skills)
	}

	// dedup second add
	if err := runAdd(addOptions{kind: "skill", value: "code-review", manifestPath: path, out: &out}); err != nil {
		t.Fatal(err)
	}
	m, _ = manifest.Read(path)
	if len(m.Skills.Bundled) != 1 {
		t.Errorf("skill duplicated: %+v", m.Skills)
	}
	if !strings.Contains(out.String(), "já presente") {
		t.Errorf("dedup message missing: %s", out.String())
	}
}

func TestRunAdd_Agent(t *testing.T) {
	dir := t.TempDir()
	writeBaseManifest(t, dir)
	path := filepath.Join(dir, manifest.Filename)
	if err := runAdd(addOptions{kind: "agent", value: "reviewer", manifestPath: path, out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Read(path)
	if len(m.Agents.Bundled) != 1 || m.Agents.Bundled[0] != "reviewer" {
		t.Errorf("agent not added: %+v", m.Agents)
	}
}

func TestRunAdd_Pipeline(t *testing.T) {
	dir := t.TempDir()
	writeBaseManifest(t, dir)
	path := filepath.Join(dir, manifest.Filename)
	if err := runAdd(addOptions{kind: "pipeline", value: "azure-pipelines", manifestPath: path, out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	m, _ := manifest.Read(path)
	if m.Pipeline.Provider != "azure-pipelines" {
		t.Errorf("pipeline not set: %+v", m.Pipeline)
	}
}

func TestRunAdd_UnknownKind(t *testing.T) {
	dir := t.TempDir()
	writeBaseManifest(t, dir)
	err := runAdd(addOptions{kind: "bogus", value: "x", manifestPath: filepath.Join(dir, manifest.Filename), out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "tipo desconhecido") {
		t.Errorf("expected unknown kind error, got %v", err)
	}
}

func TestRunAdd_MissingManifest(t *testing.T) {
	err := runAdd(addOptions{kind: "skill", value: "x", manifestPath: filepath.Join(t.TempDir(), "nope.yaml"), out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "ler manifest") {
		t.Errorf("expected read error, got %v", err)
	}
}
