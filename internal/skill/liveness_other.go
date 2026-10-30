//go:build !unix

package skill

// processLiveness reports "cannot-verify" on platforms without a signal-based
// liveness probe; recovery then requires explicit --force (documented).
func processLiveness(pid int) (string, error) {
	if pid <= 0 {
		return "invalid", nil
	}
	return "cannot-verify", nil
}
