//go:build !windows

package devmgr

import (
	"syscall"
	"time"
)

// IsRunning reports whether the project's pid process is alive (FR-9).
// A direct child that already exited stays as a zombie until reaped, and
// Kill(pid, 0) succeeds on zombies — so probe with Wait4(WNOHANG) first:
// wpid==pid means it exited, wpid==0 means still running (FR-22). Pids that
// are not our children (ECHILD) fall back to the signal-0 probe, which is
// how later `oro dev` invocations see other processes' pids.
func IsRunning(pidDir, key string) bool {
	pid, err := ReadPid(pidDir, key)
	if err != nil || pid <= 0 {
		return false
	}
	var ws syscall.WaitStatus
	wpid, werr := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	if werr == nil {
		return wpid == 0
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	return true
}

// procSysAttr puts the child in a fresh process group so Stop can signal the
// whole tree (FR-7).
func procSysAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// stopGroup terminates the project's process tree: SIGTERM to the group, then
// SIGKILL after a 3s grace period if still alive (FR-8).
func stopGroup(pid int) {
	// First a graceful SIGTERM to the whole group, then reap the child we
	// spawned and probe the group. Without reaping, the zombie stays attached
	// to the group; without probing, an adopted orphan can linger briefly and
	// NFR-3 (no process alive / port free) would be violated.
	syscall.Kill(-pid, syscall.SIGTERM)

	deadline := time.Now().Add(3 * time.Second)
	for {
		if groupGone(pid) {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		// Reap our direct child if it exited, making the group probe accurate.
		reap(pid)
		time.Sleep(50 * time.Millisecond)
	}

	// Grace expired — escalate for the whole tree, then wait for empties.
	syscall.Kill(-pid, syscall.SIGKILL)
	deadline = time.Now().Add(1 * time.Second)
	for {
		if groupGone(pid) {
			return
		}
		reap(pid)
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// groupGone reports whether no process belongs to group -pgid anymore.
func groupGone(pid int) bool {
	if err := syscall.Kill(-pid, 0); err != nil && err != syscall.EPERM {
		return true
	}
	return false
}

// groupAlive reports whether the process group still has any member — a
// dead leader with orphaned children keeps the group stoppable (FR-8).
func groupAlive(pid int) bool { return !groupGone(pid) }

// reap attempts a non-blocking wait on the direct child pid, cleaning up a
// zombie if it already exited. Returns true once the child is known gone.
func reap(pid int) (bool, error) {
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	if err == syscall.ECHILD {
		return true, nil // already reaped / nonexistent
	}
	if err == nil {
		// Wait4 returns wpid 0 when the child is still running.
		return wpid != 0, nil
	}
	return false, err
}
