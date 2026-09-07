package cli

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/devmgr"
)

// fakeDevCmd builds a bare cobra command carrying the persistent dev flags,
// mirroring what real `oro dev` exposes.
func fakeDevCmd(config string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().String("config", "", "")
	cmd.PersistentFlags().String("clients-home", "", "")
	cmd.PersistentFlags().Duration("wait", 30*time.Second, "")
	cmd.PersistentFlags().Bool("no-wait", false, "")
	if config != "" {
		cmd.PersistentFlags().Set("config", config)
	}
	return cmd
}

func fixtureDev(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dest := filepath.Join(root, "projects.config.json")
	src := filepath.Join("..", "devmgr", "testdata", "projects.config.json")
	data, err := os.ReadFile(src)
	if err != nil {
		src2 := filepath.Join("internal", "devmgr", "testdata", "projects.config.json")
		data, err = os.ReadFile(src2)
		if err != nil {
			t.Skip("no fixture: " + err.Error())
			return ""
		}
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestDevTableRendersWithoutCrash(t *testing.T) {
	cfg, err := devmgr.Load(fixtureDev(t))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writeDevTable(&buf, cfg, t.TempDir())
	out := buf.String()
	if !strings.Contains(out, "Atalho") {
		t.Fatalf("table missing header:\n%s", out)
	}
	if !strings.Contains(out, "drdelroy") {
		t.Fatalf("table missing project:\n%s", out)
	}
}

// TestDevTableUniformLineWidth é a regressão do desalinhamento: marcadores
// multibyte (▶/○) e ASCII (!) precisam manter todas as linhas da tabela com
// a mesma largura de exibição, mesmo com nomes multibyte.
func TestDevTableUniformLineWidth(t *testing.T) {
	cfg := &devmgr.Config{}
	cfg.Settings.PidDir = t.TempDir()
	cfg.Add("web", &devmgr.Project{
		Name: "ação-unição", Path: "/x", Cwd: "/x",
		PackageManager: "pnpm", DevCommand: "pnpm dev",
	})
	port := 3000
	cfg.Add("api", &devmgr.Project{
		Name: "ação-unição-çã", Path: "/y", Cwd: "/y",
		PackageManager: "npm", DevCommand: "npm run dev", Port: &port,
	})
	var buf bytes.Buffer
	writeDevTable(&buf, cfg, t.TempDir())
	out := buf.String()

	if strings.Contains(out, "\x1b[") {
		t.Fatalf("non-TTY table must not contain ANSI escapes:\n%s", out)
	}

	widths := map[int]bool{}
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.Contains(ln, "│") {
			widths[lipgloss.Width(ln)] = true
		}
	}
	if len(widths) != 1 {
		t.Fatalf("linhas da tabela com larguras diferentes %v:\n%s", widths, out)
	}
}

func TestAsciiMarker(t *testing.T) {
	cases := map[string]string{"▶": "+", "!": "!", "○": "o"}
	for in, want := range cases {
		if got := asciiMarker(in); got != want {
			t.Errorf("asciiMarker(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadDevViaConfigFlag(t *testing.T) {
	cmd := fakeDevCmd(fixtureDev(t))
	cfg, err := loadDev(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Count() != 11 {
		t.Fatalf("count = %d, want 11", cfg.Count())
	}
}

func TestResolveDevPathUsesClientsHome(t *testing.T) {
	old := os.Getenv("CLIENTES_HOME")
	os.Setenv("CLIENTES_HOME", "/tmp/home")
	defer func() {
		if old == "" {
			os.Unsetenv("CLIENTES_HOME")
		} else {
			os.Setenv("CLIENTES_HOME", old)
		}
	}()
	cmd := fakeDevCmd("")
	p, err := resolveDevPath(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if p != "/tmp/home/projects.config.json" {
		t.Fatalf("got %q", p)
	}
}

func TestDevMarker(t *testing.T) {
	cfg := &devmgr.Config{}
	cfg.Settings.PidDir = t.TempDir()
	cfg.Add("quiet", &devmgr.Project{
		Name: "q", Path: "/x", Cwd: "/x",
		PackageManager: "npm", DevCommand: "npm run dev",
	})
	proj, _ := cfg.Lookup("quiet")
	if m, pid := projectMarker(cfg, proj, "quiet"); m != "○" || pid != 0 {
		t.Fatalf("marker/pid = %q/%d, want ○/0", m, pid)
	}
}

// TestDevListShowsManagedPid: processo iniciado pelo oro mostra o PID na
// tabela; projeto parado e ocupante externo mostram '—' (FR-3/FR-4).
func TestDevListShowsManagedPid(t *testing.T) {
	work := t.TempDir()
	cfg := &devmgr.Config{}
	cfg.Settings.PidDir = filepath.Join(t.TempDir(), "pids")
	cfg.Settings.LogDir = filepath.Join(t.TempDir(), "logs")
	cfg.Add("live", &devmgr.Project{
		Name: "Live", Path: work, Cwd: work,
		PackageManager: "sh", DevCommand: "sh -c 'sleep 5'",
	})
	pid, err := devmgr.Start(cfg, "live", devmgr.BuildStartCommand("sh -c 'sleep 5'", nil, false))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = devmgr.Stop(cfg, "live") }()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
	}
	defer l.Close()
	extPort := l.Addr().(*net.TCPAddr).Port
	cfg.Add("extern", &devmgr.Project{
		Name: "Extern", Path: work, Cwd: work,
		PackageManager: "sh", DevCommand: "sh -c 'sleep 5'", Port: &extPort,
	})

	var buf bytes.Buffer
	writeDevTable(&buf, cfg, t.TempDir())
	out := buf.String()
	if !strings.Contains(out, "PID") {
		t.Fatalf("tabela sem coluna PID:\n%s", out)
	}
	if !strings.Contains(out, strconv.Itoa(pid)) {
		t.Fatalf("tabela sem o PID %d do projeto gerenciado:\n%s", pid, out)
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, " extern ") {
			if !strings.Contains(ln, "!") || strings.Contains(ln, strconv.Itoa(pid)) {
				t.Fatalf("linha do ocupante externo sem marker '!' ou com pid alheio: %q", ln)
			}
		}
		if strings.Contains(ln, " live ") && !strings.Contains(ln, strconv.Itoa(pid)) {
			t.Fatalf("linha do projeto gerenciado sem o PID %d: %q", pid, ln)
		}
	}
}


func TestDevLogPathRejectsTraversal(t *testing.T) {
	cfg := &devmgr.Config{}
	cfg.Settings.LogDir = t.TempDir()
	if _, err := devLogPath(cfg, "../evil"); err == nil {
		t.Fatal("esperado erro para atalho com traversal")
	}
	if _, err := devLogPath(cfg, "ok"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
}

func TestOpenInBrowserRejectsNonLocalURL(t *testing.T) {
	for _, url := range []string{"https://evil.example.com", "ftp://localhost:21", "http://evil.example.com:8080"} {
		if err := openInBrowser(url); err == nil {
			t.Errorf("openInBrowser(%q) = nil, want rejection", url)
		}
	}
}

func TestCleanPathArg(t *testing.T) {
	if got, err := cleanPathArg("/tmp/x/../y"); err != nil || got != "/tmp/y" {
		t.Fatalf("cleanPathArg = %q, %v", got, err)
	}
	if _, err := cleanPathArg("bad\x00path"); err == nil {
		t.Fatal("esperado erro para NUL no caminho")
	}
}

// devStartConfig writes a config with the given ported projects and returns
// its path, mirroring what `oro dev start --config` consumes.
func devStartConfig(t *testing.T, enforce bool, entries map[string]int) string {
	t.Helper()
	cfg := &devmgr.Config{}
	cfg.Settings.EnforceUniquePorts = enforce
	for k, p := range entries {
		port := p
		cfg.Add(k, &devmgr.Project{
			Name: k, Path: "/" + k, Cwd: "/" + k,
			PackageManager: "npm", DevCommand: "npm run dev", Port: &port,
		})
	}
	path := filepath.Join(t.TempDir(), "projects.config.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// devStartProjectsConfig writes a config with arbitrary dev commands (and
// optional ports), for tests that exercise real spawn + readiness behavior.
func devStartProjectsConfig(t *testing.T, entries map[string]*devmgr.Project) string {
	t.Helper()
	cfg := &devmgr.Config{}
	cfg.Settings.EnforceUniquePorts = false
	for k, p := range entries {
		cfg.Add(k, p)
	}
	path := filepath.Join(t.TempDir(), "projects.config.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// stopStartedProject kills whatever `runDevStart` spawned for key, so tests
// don't leak sleep processes.
func stopStartedProject(t *testing.T, configPath, key string) {
	t.Helper()
	cfg, err := devmgr.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if st := devmgr.Stop(cfg, key); st != devmgr.Stopped {
		t.Fatalf("stop %s = %v, want Stopped", key, st)
	}
}

// TestRunDevStartReportsDeathWithLogTail: um processo que morre no boot não
// pode ser reportado como sucesso — precisa sair como erro com hint de logs.
func TestRunDevStartReportsDeathWithLogTail(t *testing.T) {
	configPath := devStartProjectsConfig(t, map[string]*devmgr.Project{
		"morre": {Name: "morre", Path: "/", Cwd: "/", PackageManager: "npm", DevCommand: "false"},
	})
	cmd := fakeDevCmd(configPath)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := runDevStart(cmd, []string{"morre"})
	if err == nil || !strings.Contains(err.Error(), "falharam") {
		t.Fatalf("err = %v, esperado falha (processo morreu no boot)", err)
	}
	out := buf.String()
	if !strings.Contains(out, "x morre morreu durante o boot") {
		t.Fatalf("esperado 'morre morreu durante o boot', got:\n%s", out)
	}
	if !strings.Contains(out, "acompanhe com: oro dev logs morre") {
		t.Fatalf("esperado hint de logs, got:\n%s", out)
	}
	if strings.Contains(out, "+ morre") {
		t.Fatalf("morte impressa como sucesso:\n%s", out)
	}
}

// TestRunDevStartStillStartingAndAliveNoPort: boot lento com porta vira
// aviso (não falha); projeto sem porta reporta verificação limitada.
func TestRunDevStartStillStartingAndAliveNoPort(t *testing.T) {
	free := 0
	for p := 20000; p < 25000 && free == 0; p++ {
		if devmgr.PortFree(p) {
			free = p
		}
	}
	if free == 0 {
		t.Skip("no free port in test range")
	}
	port := free
	configPath := devStartProjectsConfig(t, map[string]*devmgr.Project{
		"web":    {Name: "web", Path: "/", Cwd: "/", PackageManager: "npm", DevCommand: "sleep 60", Port: &port},
		"worker": {Name: "worker", Path: "/", Cwd: "/", PackageManager: "npm", DevCommand: "sleep 60"},
	})
	defer stopStartedProject(t, configPath, "web")
	defer stopStartedProject(t, configPath, "worker")

	cmd := fakeDevCmd(configPath)
	cmd.PersistentFlags().Set("wait", "300ms")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runDevStart(cmd, []string{"web", "worker"}); err != nil {
		t.Fatalf("boot lento não deveria falhar: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "! web ainda subindo") {
		t.Fatalf("esperado aviso 'web ainda subindo', got:\n%s", out)
	}
	if !strings.Contains(out, "+ worker iniciado (PID") || !strings.Contains(out, "sem porta para verificar") {
		t.Fatalf("esperado worker iniciado sem porta, got:\n%s", out)
	}
}

// TestRunDevStartNoWaitSkipsReadiness: --no-wait preserva o comportamento
// legado de reportar logo após o spawn.
func TestRunDevStartNoWaitSkipsReadiness(t *testing.T) {
	configPath := devStartProjectsConfig(t, map[string]*devmgr.Project{
		"donothing": {Name: "donothing", Path: "/", Cwd: "/", PackageManager: "npm", DevCommand: "sleep 60"},
	})
	defer stopStartedProject(t, configPath, "donothing")

	cmd := fakeDevCmd(configPath)
	cmd.PersistentFlags().Set("no-wait", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runDevStart(cmd, []string{"donothing"}); err != nil {
		t.Fatalf("err inesperado: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "+ donothing iniciado (PID") {
		t.Fatalf("esperado start legado, got:\n%s", out)
	}
	for _, unwanted := range []string{"sem porta para verificar", "aguardando prontidão"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("--no-wait não deveria verificar prontidão (%q):\n%s", unwanted, out)
		}
	}
}

// TestDevStatusWarnsExternalOccupant: projeto parado com a porta configurada
// ocupada sai como aviso de processo não gerenciado (FR-4), e não como o
// genérico "nenhum projeto rodando".
func TestDevStatusWarnsExternalOccupant(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	configPath := devStartConfig(t, false, map[string]int{"web": port})

	cmd := fakeDevCmd(configPath)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := newDevStatusCmd().RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "porta "+strconv.Itoa(port)+" ocupada por processo externo") {
		t.Fatalf("esperado aviso de ocupante externo na porta %d, got:\n%s", port, out)
	}
	if strings.Contains(out, "nenhum projeto rodando") {
		t.Fatalf("ocupante externo não deve virar 'nenhum projeto rodando':\n%s", out)
	}
}

// TestDevStopReportsExternalOccupant: stop de projeto não gerenciado com a
// porta ocupada informa o ocupante externo (FR-7), sem sinalizar ninguém.
func TestDevStopReportsExternalOccupant(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	configPath := devStartConfig(t, false, map[string]int{"web": port})

	cmd := fakeDevCmd(configPath)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := runDevStop(cmd, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "porta "+strconv.Itoa(port)+" ocupada por processo externo") {
		t.Fatalf("esperado aviso de ocupante externo no stop, got:\n%s", out)
	}
	if strings.Contains(out, "+ web") {
		t.Fatalf("stop de projeto inexistente impresso como sucesso:\n%s", out)
	}
}

func TestLastLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(p, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lastLines(p, 2); len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("lastLines = %v, want [b c]", got)
	}
	if got := lastLines(p, 10); len(got) != 3 {
		t.Fatalf("lastLines = %v, want 3 linhas", got)
	}
	if got := lastLines(filepath.Join(t.TempDir(), "missing.log"), 5); got != nil {
		t.Fatalf("lastLines em arquivo inexistente = %v, want nil", got)
	}
	// arquivo maior que o chunk de leitura: só o final importa
	big := make([]byte, 0, 8192)
	for i := 0; i < 300; i++ {
		big = append(big, []byte(fmt.Sprintf("linha-%03d\n", i))...)
	}
	if err := os.WriteFile(p, big, 0o644); err != nil {
		t.Fatal(err)
	}
	got := lastLines(p, 3)
	if len(got) != 3 || got[2] != "linha-299" {
		t.Fatalf("lastLines em arquivo grande = %v, want final linha-297..299", got)
	}
}

// TestRunDevStartReportsFailureAsError garante que falhas do start saem com
// marcador de erro (x em não-TTY), não como sucesso.
func TestRunDevStartReportsFailureAsError(t *testing.T) {
	configPath := devStartConfig(t, false, map[string]int{})
	cmd := fakeDevCmd(configPath)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := runDevStart(cmd, []string{"fantasma"})
	if err == nil || !strings.Contains(err.Error(), "falharam") {
		t.Fatalf("err = %v, esperado falha reportada", err)
	}
	out := buf.String()
	if !strings.Contains(out, "x fantasma não encontrado") {
		t.Fatalf("esperado 'x fantasma não encontrado', got:\n%s", out)
	}
	if strings.Contains(out, "+ fantasma") {
		t.Fatalf("falha impressa como sucesso:\n%s", out)
	}
}

func TestRunDevStartBlocksPortInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
		return
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	configPath := devStartConfig(t, true, map[string]int{"web": port})
	cmd := fakeDevCmd(configPath)
	err = runDevStart(cmd, []string{"web"})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("err = %v, esperado bloqueio de porta ocupada no start", err)
	}
	// nada pode ter sido iniciado: o pre-check falha antes das goroutines
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(configPath), ".dev-pids")); statErr == nil {
		t.Error("nenhum processo deveria ter sido iniciado")
	}
}

func TestRunDevStartBlocksDuplicatePortsInBatch(t *testing.T) {
	free := 0
	for p := 20000; p < 25000 && free == 0; p++ {
		if devmgr.PortFree(p) {
			free = p
		}
	}
	if free == 0 {
		t.Skip("no free port in test range")
		return
	}
	configPath := devStartConfig(t, true, map[string]int{"a": free, "b": free})
	cmd := fakeDevCmd(configPath)
	err := runDevStart(cmd, []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "already assigned") {
		t.Fatalf("err = %v, esperado bloqueio de porta duplicada no lote", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(configPath), ".dev-pids")); statErr == nil {
		t.Error("nenhum processo deveria ter sido iniciado")
	}
}