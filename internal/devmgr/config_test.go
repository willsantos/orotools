package devmgr

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) string {
	root := t.TempDir()
	dest := filepath.Join(root, "projects.config.json")
	src := filepath.Join("testdata", "projects.config.json")
	copyFixture(t, src, dest)
	return dest
}

func copyFixture(t *testing.T, src, dest string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal("read fixture: "+err.Error())
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatal("write fixture copy: "+err.Error())
	}
}

func TestRoundTripByteIdentical(t *testing.T) {
	path := fixture(t)
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read orig: "+err.Error())
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal("load: "+err.Error())
	}
	out, err := cfg.Marshal()
	if err != nil {
		t.Fatal("marshal: "+err.Error())
	}
	if !bytes.Equal(orig, out) {
		t.Fatalf("round-trip not byte-identical\n--- orig ---\n%s\n--- out ----\n%s",
			orig, out)
	}
}

func TestCountProjects(t *testing.T) {
	cfg, err := Load(fixture(t))
	if err != nil {
		t.Fatal(err.Error())
	}
	if cfg.Count() != 11 {
		t.Fatalf("count = %d, want 11", cfg.Count())
	}
}

func TestOrderPreserved(t *testing.T) {
	cfg, err := Load(fixture(t))
	if err != nil {
		t.Fatal(err.Error())
	}
	keys := cfg.Keys()
	if len(keys) < 3 {
		t.Fatalf("too few keys: %v", keys)
	}
	// fixture order: beafaes, contabil, delroyt ...
	want := []string{"beafaes", "contabil", "delroyt"}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("key[%d]=%q want %q (order mismatch)", i, keys[i], k)
		}
	}
}

func TestLookupProject(t *testing.T) {
	cfg, err := Load(fixture(t))
	if err != nil {
		t.Fatal(err.Error())
	}
	p, ok := cfg.Lookup("drdelroy")
	if !ok {
		t.Fatal("project drdelroy not found")
	}
	if p.Name != "Dr. Delroy (Site)" {
		t.Fatalf("name = %q", p.Name)
	}
	if p.Port == nil || *p.Port != 3004 {
		t.Fatalf("port drdelroy = %v, want 3004", p.Port)
	}
}

func TestAddPreservesPosition(t *testing.T) {
	cfg, err := Load(fixture(t))
	if err != nil {
		t.Fatal(err.Error())
	}
	n := cfg.Count()
	cfg.Add("novo", &Project{Name: "Novo", Path: "/x", Cwd: "/x",
		PackageManager: "npm", DevCommand: "npm run dev"})
	if cfg.Count() != n+1 {
		t.Fatalf("count = %d, want %d", cfg.Count(), n+1)
	}
	keys := cfg.Keys()
	if keys[len(keys)-1] != "novo" {
		t.Fatalf("new key not appended")
	}
}

func TestResolveConfigPath(t *testing.T) {
	got, err := ResolveConfigPath("/home/x/clientes", "")
	if err != nil {
		t.Fatal(err.Error())
	}
	if got != "/home/x/clientes/projects.config.json" {
		t.Fatalf("got %q", got)
	}
	got2, err := ResolveConfigPath("", "/tmp/custom.json")
	if err != nil {
		t.Fatal(err.Error())
	}
	if got2 != "/tmp/custom.json" {
		t.Fatalf("override got %q", got2)
	}
	_, err = ResolveConfigPath("", "")
	if err == nil {
		t.Fatal("expected error without CLIENTES_HOME")
	}
}