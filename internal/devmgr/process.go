package devmgr

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// keyValid rejects shortcuts that could escape the pid/log directories via
// path traversal — the policy the CLI enforced for logs, now enforced where
// the files are actually written and read (FR-8).
func keyValid(key string) bool {
	return key != "" && key != "." && key != ".." &&
		!strings.ContainsAny(key, `/\`) && !strings.ContainsRune(key, '\x00')
}

// PidFile returns the pid file path for key under pidDir, rejecting keys
// with path traversal (FR-8).
func PidFile(pidDir, key string) (string, error) {
	return keyedPath(pidDir, key, ".pid", "pid")
}

// LogFile returns the log file path for key under logDir, rejecting keys
// with path traversal (FR-8).
func LogFile(logDir, key string) (string, error) {
	return keyedPath(logDir, key, ".log", "log")
}

func keyedPath(dir, key, ext, kind string) (string, error) {
	if !keyValid(key) {
		return "", fmt.Errorf("devmgr: atalho inválido para arquivo de %s: %q", kind, key)
	}
	joined := filepath.Join(dir, key+ext)
	rel, err := filepath.Rel(dir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("devmgr: caminho de %s escapa do diretório: %q", kind, joined)
	}
	return joined, nil
}

// ReadPid reads and parses the pid stored in a pid file.
func ReadPid(pidDir, key string) (int, error) {
	pp, err := PidFile(pidDir, key)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(pp)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, errors.New("devmgr: bad pid file " + pp)
	}
	return pid, nil
}

// ProcState is the outcome of inspecting a project's pid file.
type ProcState struct {
	// Running reports whether the project has a live managed process:
	// alive and — when the platform can decide — matching the project.
	Running bool
	// PID is the pid read from the pid file (0 when missing/unparsable).
	PID int
	// Stale reports whether a stale pid file was collected during the
	// check: dead pid, or a foreign process that reused the pid (FR-5).
	Stale bool
}

// CheckProcess inspects the project's pid file: liveness plus — when the
// platform can decide — identity against the project's work dir (FR-6). A
// dead or foreign pid is treated as not running and the pid file is
// collected (FR-5), re-reading it just before the unlink so a concurrent
// start that just rewrote it is not clobbered.
func CheckProcess(cfg *Config, key string) ProcState {
	pp, err := PidFile(cfg.PidDir(), key)
	if err != nil {
		return ProcState{}
	}
	raw, err := os.ReadFile(pp)
	if err != nil {
		return ProcState{} // no pid file: nothing managed, nothing to collect
	}
	st := ProcState{}
	if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil {
		st.PID = pid
	}
	collect := func() {
		if cur, rerr := os.ReadFile(pp); rerr == nil && string(cur) == string(raw) {
			os.Remove(pp)
		}
	}
	if st.PID <= 0 || !IsRunning(cfg.PidDir(), key) {
		collect()
		st.Stale = true
		return st
	}
	if !procIsOurs(st.PID, cfg, key) {
		collect()
		st.Stale = true
		return st
	}
	st.Running = true
	return st
}

// procIsOurs reports whether a live pid is the project's managed process by
// comparing /proc/<pid>/cwd with the expected work dir (FR-6, best effort on
// Linux). Undecidable platforms and projects without a comparable dir trust
// the liveness probe.
func procIsOurs(pid int, cfg *Config, key string) bool {
	p, ok := cfg.Lookup(key)
	if !ok {
		return false // unknown project: cannot confirm identity
	}
	want := p.WorkDir()
	if want == "" {
		return true
	}
	cwd, ok := procCwd(pid)
	if !ok {
		return true // undecidable: degrade to the liveness probe (FR-6)
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	} else {
		want = filepath.Clean(want)
	}
	return filepath.Clean(cwd) == want
}

// Start launches the dev server detached in a fresh process group, wiring
// stdout/stderr to the project log, and persists the pid file (FR-7).
// The returned pid is both the process id and its process group id on Unix.
func Start(cfg *Config, key string, cmd StartCommand) (int, error) {
	p, ok := cfg.Lookup(key)
	if !ok {
		return 0, errors.New("devmgr: project not found: " + key)
	}
	if err := ensureDir(cfg.PidDir()); err != nil {
		return 0, err
	}
	if err := ensureDir(cfg.LogDir()); err != nil {
		return 0, err
	}

	logFile, err := LogFile(cfg.LogDir(), key)
	if err != nil {
		return 0, err
	}
	logF, err := os.Create(logFile)
	if err != nil {
		return 0, err
	}
	// resolve the executable via PATH
	bin := cmd.Args[0]
	if resolved, err := exec.LookPath(bin); err == nil {
		bin = resolved
	}

	attr := &os.ProcAttr{
		Dir:   p.WorkDir(),
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
	pp, err := PidFile(cfg.PidDir(), key)
	if err != nil {
		return pid, err
	}
	if err := os.WriteFile(pp,
		[]byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return pid, err
	}
	return pid, nil
}

// StopState is the outcome of Stop, letting the CLI report stale/foreign
// pid files instead of silently doing nothing (FR-7).
type StopState int

const (
	// Stopped means a live managed process tree was signaled and the pid
	// file removed.
	Stopped StopState = iota
	// NotRunning means there was no pid file, or it pointed at a dead pid;
	// any leftover pid file was collected.
	NotRunning
	// StaleForeign means the pid file pointed at a live process that is not
	// the project's (pid reuse / foreign owner): it was left unsignaled and
	// the pid file removed.
	StaleForeign
)

// Stop terminates the project's process tree and removes the pid file
// (FR-8) — eliminates orphans that motivated --force. A live pid that fails
// the identity check (FR-6) is never signaled: it belongs to an unrelated
// process that reused the pid, and Stop reports StaleForeign instead. The
// escalation strategy (signals on Unix, taskkill on Windows) lives in the
// platform files.
func Stop(cfg *Config, key string) StopState {
	pp, err := PidFile(cfg.PidDir(), key)
	if err != nil {
		return NotRunning
	}
	raw, err := os.ReadFile(pp)
	if err != nil {
		return NotRunning
	}
	remove := func() {
		if cur, rerr := os.ReadFile(pp); rerr == nil && string(cur) == string(raw) {
			os.Remove(pp)
		}
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if perr != nil || pid <= 0 {
		remove()
		return NotRunning
	}
	// A live leader must pass the identity gate before any signal (FR-6).
	if IsRunning(cfg.PidDir(), key) && !procIsOurs(pid, cfg, key) {
		remove()
		return StaleForeign
	}
	// The leader may be dead while orphaned children keep the group alive —
	// the group signal is what stops them (FR-8). With the whole group gone
	// there is nothing to stop.
	if !groupAlive(pid) {
		remove()
		return NotRunning
	}
	stopGroup(pid)
	remove()
	return Stopped
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
