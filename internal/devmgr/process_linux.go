//go:build linux

package devmgr

import (
	"fmt"
	"os"
)

// procCwd returns the work directory of a live pid via /proc, used to tell
// our managed dev servers from an unrelated process that reused the pid
// (FR-6). ok=false means undecidable (no /proc, EACCES) — the caller then
// trusts the liveness probe.
func procCwd(pid int) (string, bool) {
	s, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return "", false
	}
	return s, true
}
