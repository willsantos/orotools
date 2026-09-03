package devmgr

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// pidPath returns the pid file path for a project under pidDir.
func pidPath(pidDir, key string) string {
	return filepath.Join(pidDir, key + ".pid")
}

// logPath returns the log file path for a project under logDir.
func logPath(logDir, key string) string {
	return filepath.Join(logDir, key + ".log")
}

// ReadPid reads and parses the pid stored in a pid file.
func ReadPid(pidDir, key string) (int, error) {
	data, err := os.ReadFile(pidPath(pidDir, key))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, errors.New("devmgr: bad pid file " + pidPath(pidDir, key))
	}
	return pid, nil
}

// Start launches the dev server detached in a fresh process group, wiring
// stdout/stderr to the project log, and persists the pid file (FR-7).
// The returned pid is both the process id and its process group id on Unix.
func Start(cfg *Config, key string, cmd StartCommand) (int, error) {
	p, ok := cfg.Lookup(key)
	if !ok {
		return 0, errors.New("devmgr: project not found: " + key)
	}
	if err := ensureDir(cfg.Settings.PidDir); err != nil {
		return 0, err
	}
	if err := ensureDir(cfg.Settings.LogDir); err != nil {
		return 0, err
	}

	logPath := logPath(cfg.Settings.LogDir, key)
	logF, err := os.Create(logPath)
	if err != nil {
		return 0, err
	}
	// resolve the executable via PATH
	bin := cmd.Args[0]
	if resolved, err := exec.LookPath(bin); err == nil {
		bin = resolved
	}

	attr := &os.ProcAttr{
		Dir:   p.Cwd,
		Env:   mergeEnv(cmd.Env),
		Files: []*os.File{nil, logF, logF},
		Sys:   procSysAttr(),
	}
	proc, err := os.StartProcess(bin, cmd.Args, attr)
	logF.Close()
	if err != nil {
		return 0, err
	}
	pid := proc.Pid
	if err := os.WriteFile(pidPath(cfg.Settings.PidDir, key),
		[]byte(strconv.Itoa(pid) + "\n"), 0o644); err != nil {
		return pid, err
	}
	return pid, nil
}

// Stop terminates the project's process tree and removes the pid file.
// FR-8 — eliminates orphans that motivated --force. The escalation strategy
// (signals on Unix, taskkill on Windows) lives in the platform files.
func Stop(pidDir, key string) error {
	pid, err := ReadPid(pidDir, key)
	if err != nil {
		os.Remove(pidPath(pidDir, key))
		return nil
	}
	if pid > 0 {
		stopGroup(pid)
	}
	os.Remove(pidPath(pidDir, key))
	return nil
}

func ensureDir(dir string) error {
	if dir == "" {
		return nil
	}
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return errors.New("devmgr: not a directory: " + dir)
		}
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func mergeEnv(base []string) []string {
	cur := os.Environ()
	return append(cur, base...)
}
