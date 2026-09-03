//go:build !windows

package devmgr

import (
	"os"
	"path/filepath"
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
	defer func() { _ = Stop(cfg.Settings.PidDir, "test") }()

	if _, err := os.Stat(pidPath(cfg.Settings.PidDir, "test")); err != nil {
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

	if err := Stop(cfg.Settings.PidDir, "test"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Fatalf("process group %d still alive after stop", pid)
	}
	if IsRunning(cfg.Settings.PidDir, "test") {
		t.Fatalf("is_running still true after stop")
	}
	if _, err := os.Stat(pidPath(cfg.Settings.PidDir, "test")); err == nil {
		t.Fatalf("pid file not removed after stop")
	}
}

func TestStopCleansStalePidFile(t *testing.T) {
	cfg, _ := newTestConfig(t, "sh -c 'sleep 5'")
	if err := os.MkdirAll(cfg.Settings.PidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath(cfg.Settings.PidDir, "test"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stop(cfg.Settings.PidDir, "test"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(pidPath(cfg.Settings.PidDir, "test")); err == nil {
		t.Fatalf("stale pid file not removed")
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
	defer func() { _ = Stop(cfg.Settings.PidDir, "test") }()
	time.Sleep(300 * time.Millisecond) // wait for shell to write

	data, err := os.ReadFile(filepath.Join(work, "env.txt"))
	if err != nil {
		t.Fatalf("read env.txt: %v", err)
	}
	if strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf("PORT env = %q, want 4242", string(data))
	}
}