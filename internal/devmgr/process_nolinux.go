//go:build !linux

package devmgr

// procCwd is unavailable off Linux: identity is undecidable and callers
// trust the liveness probe (FR-6 degradation).
func procCwd(pid int) (string, bool) { return "", false }
