//go:build unix

package skill

import "syscall"

// processLiveness reports whether a pid is verifiably alive on unix: "alive",
// "dead" or "cannot-verify". A process that exists but is not ours (EPERM) is
// alive; ENOENT/ESRCH means the pid is gone.
func processLiveness(pid int) (string, error) {
	if pid <= 0 {
		return "invalid", nil
	}
	err := syscall.Kill(pid, 0)
	switch err {
	case nil:
		return "alive", nil
	case syscall.ESRCH:
		return "dead", nil
	case syscall.EPERM:
		return "alive", nil
	default:
		return "cannot-verify", err
	}
}
