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

// TestResolveConfigPathAbsAnchor: CLIENTES_HOME relativo não pode gerar
// âncora relativa para os dirs de pid/log — o resultado é sempre absoluto
// (dev-pid-tracking FR-1).
func TestResolveConfigPathAbsAnchor(t *testing.T) {
	got, err := ResolveConfigPath("rel/clientes", "")
	if err != nil {
		t.Fatal(err.Error())
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("got %q, want absolute", got)
	}
	if filepath.Base(got) != "projects.config.json" {
		t.Fatalf("got %q", got)
	}
}

// writeDirsConfig writes a minimal config with the given raw settings JSON
// and returns its path.
func writeDirsConfig(t *testing.T, base, settings string) string {
	t.Helper()
	path := filepath.Join(base, "projects.config.json")
	config := `{"projects":{},"settings":` + settings + `}`
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadAnchorsDirsIndependently: cada campo é resolvido por si — vazio
// vira o default, relativo ancora no dir do config (dev-pid-tracking FR-1).
func TestLoadAnchorsDirsIndependently(t *testing.T) {
	base := t.TempDir()
	cfg, err := Load(writeDirsConfig(t, base,
		`{"log_dir":"logs","pid_dir":"","enforce_unique_ports":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.PidDir(), filepath.Join(base, ".dev-pids"); got != want {
		t.Fatalf("pid dir = %q, want %q (pid_dir vazio vira default)", got, want)
	}
	if got, want := cfg.LogDir(), filepath.Join(base, "logs"); got != want {
		t.Fatalf("log dir = %q, want %q (relativo ancora no dir do config)", got, want)
	}
	if cfg.Settings.PidDir != "" || cfg.Settings.LogDir != "logs" {
		t.Fatalf("Settings devem preservar os valores crus: %+v", cfg.Settings)
	}
}

func TestLoadKeepsAbsoluteDirs(t *testing.T) {
	base := t.TempDir()
	pidDir := filepath.Join(t.TempDir(), "abs-pids")
	cfg, err := Load(writeDirsConfig(t, base,
		`{"log_dir":"logs","pid_dir":"`+pidDir+`","enforce_unique_ports":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PidDir(); got != pidDir {
		t.Fatalf("pid dir = %q, want %q (absoluto inalterado)", got, pidDir)
	}
}

// TestMarshalKeepsRawDirs: Save/Marshal nunca persistem os caminhos
// resolvidos — o config no disco continua com os valores crus (NFR-4).
func TestMarshalKeepsRawDirs(t *testing.T) {
	base := t.TempDir()
	cfg, err := Load(writeDirsConfig(t, base,
		`{"log_dir":"logs","pid_dir":".dev-pids","enforce_unique_ports":false}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`".dev-pids"`)) || !bytes.Contains(out, []byte(`"logs"`)) {
		t.Fatalf("marshal perdeu os valores crus:\n%s", out)
	}
	if bytes.Contains(out, []byte(base)) {
		t.Fatalf("marshal vazou caminho resolvido:\n%s", out)
	}
}

func TestProjectWorkDirFallsBackToPath(t *testing.T) {
	p := &Project{Path: "/base"}
	if got := p.WorkDir(); got != "/base" {
		t.Fatalf("WorkDir = %q, want /base", got)
	}
	p.Cwd = "/custom"
	if got := p.WorkDir(); got != "/custom" {
		t.Fatalf("WorkDir = %q, want /custom", got)
	}
}