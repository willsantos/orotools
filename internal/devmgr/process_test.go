//go:build !windows

package devmgr

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newTestConfig(t *testing.T, devCommand string) (*Config, string) {
	t.Helper()
	pidDir := filepath.Join(t.TempDir(), ".dev-pids")
	logDir := filepath.Join(t.TempDir(), ".dev-logs")
	work := t.TempDir()

	cfg := &Config{}
	cfg.Settings.PidDir = pidDir
	cfg.Settings.LogDir = logDir
	cfg.Add("test", &Project{
		Name:           "Test",
		Path:           work,
		Cwd:            work,
		PackageManager: "sh",
		DevCommand:     devCommand,
	})
	return cfg, work
}

func TestStartWritesPidAndIsRunning(t *testing.T) {
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	sc := BuildStartCommand("sh -c 'sleep 5'", nil, false)

	pid, err := Start(cfg, "test", sc)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = Stop(cfg, "test") }()

	if _, err := os.Stat(mustPidFile(t, cfg, "test")); err != nil {
		t.Fatalf("pid file missing: %v", err)
	}
	if !IsRunning(cfg.Settings.PidDir, "test") {
		t.Fatalf("process pid %d should be running", pid)
	}
}

func TestStopKillsProcessTree(t *testing.T) {
	cfg, _ := newTestConfig(t, "sh -c '(sleep 30 &); wait 30'")
	sc := BuildStartCommand("sh -c '(sleep 30 &); wait 30'", nil, false)

	pid, err := Start(cfg, "test", sc)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !IsRunning(cfg.Settings.PidDir, "test") {
		t.Fatalf("pid %d should run", pid)
	}
	time.Sleep(300 * time.Millisecond) // let the grandchild spawn

	if st := Stop(cfg, "test"); st != Stopped {
		t.Fatalf("stop = %v, want Stopped", st)
	}
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Fatalf("process group %d still alive after stop", pid)
	}
	if IsRunning(cfg.Settings.PidDir, "test") {
		t.Fatalf("is_running still true after stop")
	}
	if _, err := os.Stat(mustPidFile(t, cfg, "test")); err == nil {
		t.Fatalf("pid file not removed after stop")
	}
}

func TestStopCleansStalePidFile(t *testing.T) {
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	if err := os.MkdirAll(cfg.Settings.PidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mustPidFile(t, cfg, "test"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := Stop(cfg, "test"); st != NotRunning {
		t.Fatalf("stop = %v, want NotRunning", st)
	}
	if _, err := os.Stat(mustPidFile(t, cfg, "test")); err == nil {
		t.Fatalf("stale pid file not removed")
	}
}

// TestStopForeignPidNotSignaled: pid alheio vivo nunca recebe sinal — o pid
// file é coletado e StaleForeign reportado (dev-pid-tracking FR-6/FR-7).
func TestStopForeignPidNotSignaled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("identidade via /proc é linux-only")
	}
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	other := t.TempDir()
	pid, stopSleep := spawnSleep(t, other)
	defer stopSleep()
	writeTestPid(t, cfg, "test", pid)

	if st := Stop(cfg, "test"); st != StaleForeign {
		t.Fatalf("stop = %v, want StaleForeign", st)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("processo alheio foi sinalizado: %v", err)
	}
	if _, err := os.Stat(mustPidFile(t, cfg, "test")); !os.IsNotExist(err) {
		t.Fatal("pid file alheio não foi coletado")
	}
}

func TestStartInjectsPortEnv(t *testing.T) {
	work := t.TempDir()
	cfg := &Config{}
	cfg.Settings.PidDir = filepath.Join(t.TempDir(), "pids")
	cfg.Settings.LogDir = filepath.Join(t.TempDir(), "logs")
	cfg.Add("test", &Project{
		Name: "Test", Path: work, Cwd: work,
		PackageManager: "sh",
		DevCommand:     "sh -c 'echo ${PORT} > env.txt; sleep 5'",
	})
	sc := StartCommand{Args: []string{"sh", "-c", "echo ${PORT} > env.txt; sleep 5"}, Env: []string{"PORT=4242"}}
	if _, err := Start(cfg, "test", sc); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = Stop(cfg, "test") }()
	time.Sleep(300 * time.Millisecond) // wait for shell to write

	data, err := os.ReadFile(filepath.Join(work, "env.txt"))
	if err != nil {
		t.Fatalf("read env.txt: %v", err)
	}
	if strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf("PORT env = %q, want 4242", string(data))
	}
}

// TestStartUsesWorkDirFallback: sem cwd no config, o processo nasce no path
// do projeto — nunca no CWD da invocação (dev-pid-tracking FR-2).
func TestStartUsesWorkDirFallback(t *testing.T) {
	work := t.TempDir()
	cfg := &Config{}
	cfg.Settings.PidDir = filepath.Join(t.TempDir(), "pids")
	cfg.Settings.LogDir = filepath.Join(t.TempDir(), "logs")
	cfg.Add("test", &Project{
		Name: "Test", Path: work,
		PackageManager: "sh",
		DevCommand:     "sh -c 'pwd -P > cwd.txt; sleep 5'",
	})
	sc := StartCommand{Args: []string{"sh", "-c", "pwd -P > cwd.txt; sleep 5"}}
	if _, err := Start(cfg, "test", sc); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = Stop(cfg, "test") }()
	time.Sleep(300 * time.Millisecond) // wait for shell to write

	data, err := os.ReadFile(filepath.Join(work, "cwd.txt"))
	if err != nil {
		t.Fatalf("read cwd.txt: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("child cwd = %q, want %q", got, want)
	}
}

// mustPidFile returns the pid file path for key in cfg's pid dir.
func mustPidFile(t *testing.T, cfg *Config, key string) string {
	t.Helper()
	pp, err := PidFile(cfg.PidDir(), key)
	if err != nil {
		t.Fatal(err)
	}
	return pp
}

// writeTestPid writes a raw pid file for key in cfg's pid dir.
func writeTestPid(t *testing.T, cfg *Config, key string, pid int) string {
	t.Helper()
	pp := mustPidFile(t, cfg, key)
	if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pp, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pp
}

// TestCheckProcessCollectsDeadPid: pid file apontando para processo morto é
// coletado e o projeto sai como não rodando (dev-pid-tracking FR-5).
func TestCheckProcessCollectsDeadPid(t *testing.T) {
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	pp := writeTestPid(t, cfg, "test", dead.Process.Pid)

	st := CheckProcess(cfg, "test")
	if st.Running || !st.Stale {
		t.Fatalf("state = %+v, want not running + stale", st)
	}
	if st.PID != dead.Process.Pid {
		t.Fatalf("pid = %d, want %d", st.PID, dead.Process.Pid)
	}
	if _, err := os.Stat(pp); !os.IsNotExist(err) {
		t.Fatalf("pid file não foi coletado: %v", err)
	}
}

func TestCheckProcessWithoutPidFile(t *testing.T) {
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	if st := CheckProcess(cfg, "test"); st.Running || st.Stale || st.PID != 0 {
		t.Fatalf("state = %+v, want zero (sem pid file)", st)
	}
}

// spawnSleep starts a long-lived `sleep` with cwd = dir; the returned stop
// func kills and reaps it.
func spawnSleep(t *testing.T, dir string) (int, func()) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	return pid, func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// TestCheckProcessCollectsForeignPid: pid alheio vivo (cwd divergente) é
// tratado como não rodando, o pid file é coletado e o processo alheio fica
// intacto (dev-pid-tracking FR-6).
func TestCheckProcessCollectsForeignPid(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("identidade via /proc é linux-only")
	}
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	other := t.TempDir()
	pid, stop := spawnSleep(t, other)
	defer stop()
	pp := writeTestPid(t, cfg, "test", pid)

	st := CheckProcess(cfg, "test")
	if st.Running || !st.Stale || st.PID != pid {
		t.Fatalf("state = %+v, want not running + stale com pid %d", st, pid)
	}
	if _, err := os.Stat(pp); !os.IsNotExist(err) {
		t.Fatalf("pid file alheio não foi coletado: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("processo alheio foi sinalizado: %v", err)
	}
}

// TestCheckProcessRunningWithSymlinkedWorkDir: o kernel devolve o caminho
// real em /proc/<pid>/cwd; WorkDir com symlink precisa resolver antes de
// comparar, senão projeto saudável aparece como parado.
func TestCheckProcessRunningWithSymlinkedWorkDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("identidade via /proc é linux-only")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	cfg.Settings.PidDir = filepath.Join(t.TempDir(), "pids")
	cfg.Settings.LogDir = filepath.Join(t.TempDir(), "logs")
	cfg.Add("test", &Project{
		Name: "Test", Path: link, Cwd: link,
		PackageManager: "sh", DevCommand: "sh -c 'sleep 5'",
	})
	pid, err := Start(cfg, "test", BuildStartCommand("sh -c 'sleep 5'", nil, false))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = Stop(cfg, "test") }()

	st := CheckProcess(cfg, "test")
	if !st.Running || st.PID != pid {
		t.Fatalf("state = %+v, want running com pid %d", st, pid)
	}
}
// TestPidFileLogFileRejectTraversal: atalhos com path traversal são
// rejeitados onde os arquivos são de fato escritos e lidos (FR-8).
func TestPidFileLogFileRejectTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, k := range []string{"", ".", "..", "../x", "a/b", `a\b`, "a\x00b"} {
		if _, err := PidFile(dir, k); err == nil {
			t.Errorf("PidFile(%q) aceitou atalho inválido", k)
		}
		if _, err := LogFile(dir, k); err == nil {
			t.Errorf("LogFile(%q) aceitou atalho inválido", k)
		}
	}
	if p, err := PidFile(dir, "api"); err != nil || p != filepath.Join(dir, "api.pid") {
		t.Fatalf("PidFile(api) = %q, %v", p, err)
	}
	if p, err := LogFile(dir, "api"); err != nil || p != filepath.Join(dir, "api.log") {
		t.Fatalf("LogFile(api) = %q, %v", p, err)
	}
}
