//go:build windows

package devmgr

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// IsRunning reports whether the project's pid process is alive (FR-9).
// Windows has no signal-0 probe, so query tasklist by PID filter instead.
func IsRunning(pidDir, key string) bool {
	pid, err := ReadPid(pidDir, key)
	if err != nil || pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist",
		"/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	// CSV rows quote every field; an unmatched filter prints an INFO line
	// instead of a row, so requiring the quoted pid avoids substring hits.
	return strings.Contains(string(out), `"`+strconv.Itoa(pid)+`"`)
}

// procSysAttr returns the default Windows process attributes — there are no
// process groups, so Stop relies on taskkill's tree walk instead (FR-7).
func procSysAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// stopGroup kills the process tree via taskkill /T /F — the Windows
// equivalent of the SIGTERM → SIGKILL escalation (FR-8 best effort: Windows
// has no signals, so there is no grace period).
func stopGroup(pid int) {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

// groupAlive is always true on Windows: there are no process groups to
// probe, and taskkill on a dead pid is a harmless no-op.
func groupAlive(pid int) bool { return true }
