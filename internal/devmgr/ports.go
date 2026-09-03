package devmgr

import (
	"fmt"
	"net"
	"strings"
)

// PortRange is a closed interval of candidate ports.
type PortRange struct {
	Start, End int
}

// portRange is a closed interval of candidate ports.
type portRange struct{ Start, End int }

// Sequences preserved from the bash Dev Manager:
// 3001-3099  next/full-stack (3000 series to avoid colliding with tools)
// 5174-5199  vite/bundlers (Vite defaults)
var nextRange = portRange{3001, 3099}
var viteRange = portRange{5174, 5199}

// DevRange returns the port range for a dev command (FR-6).
func DevRange(devCommand string) PortRange {
	switch {
	case strings.Contains(devCommand, "vite"):
		return PortRange(viteRange)
	case strings.Contains(devCommand, "next") || strings.Contains(devCommand, "nextjs"):
		return PortRange(nextRange)
	default:
		// Wrangler workers / generic: default next range; caller decides.
		return PortRange(nextRange)
	}
}

// AssignedPorts returns port→slug from cfg for NextFreePort suggestions.
func AssignedPorts(cfg *Config) map[int]string {
	return assignedPorts(cfg, "")
}

// PortInUse reports whether a TCP listener can be bound on port.
// Uses net.Listen + immediate close — no external binaries (FR-4).
func PortInUse(port int) bool {
	if port <= 0 {
		return false
	}
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// PortFree is the inverse of PortInUse.
func PortFree(port int) bool { return !PortInUse(port) }

// NextFreePort returns the lowest free port inside r following the
// "unique" policy: skip ports already assigned to other projects in cfg.
// A free port is one that is both listeneable and unassigned.
func NextFreePort(r PortRange, assigned map[int]string) (int, bool) {
	for p := r.Start; p <= r.End; p++ {
		if _, used := assigned[p]; used {
			continue
		}
		if PortFree(p) {
			return p, true
		}
	}
	return 0, false
}

// assignedPorts builds the port→slug map from cfg (excluding exclude).
func assignedPorts(cfg *Config, exclude string) map[int]string {
	m := map[int]string{}
	for k, p := range cfg.projects {
		if k == exclude {
			continue
		}
		if p.Port != nil {
			m[*p.Port] = k
		}
	}
	return m
}

// TypeOfCommand maps a dev_command to its port range sequence (FR-6).
// Vite/bundler commands get the 5174-5199 range; everything else (next,
// full-stack, generic) gets 3001-3099. Wrangler/manifest workers are null
// and handled by the caller.
func TypeOfCommand(devCommand string) (r portRange) {
	if containsAny(devCommand, "vite") {
		return viteRange
	}
	return nextRange
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ValidateUniquePorts returns an error if two projects share the same
// non-nil port (FR-6, used by the wizard to reject duplicates).
func ValidateUniquePorts(cfg *Config, key string, port int) error {
	assigned := assignedPorts(cfg, key)
	for p, slug := range assigned {
		if p == port {
			return fmt.Errorf("port %d already assigned to project %q", port, slug)
		}
	}
	return nil
}

// CheckStartPorts enforces the FR-6 pre-start block over a batch of starts.
// Every not-yet-running project with a configured port must have a bindable
// port and — when Settings.EnforceUniquePorts is set — one not assigned to
// any other project, including others in the same batch. Callers must run it
// before spawning the start goroutines so a parallel batch cannot race past
// the validation.
func CheckStartPorts(cfg *Config, keys []string) error {
	for _, k := range keys {
		p, ok := cfg.Lookup(k)
		if !ok || p.Port == nil {
			continue // unknown keys and portless workers surface in Start's path
		}
		if IsRunning(cfg.Settings.PidDir, k) {
			continue // a running project legitimately holds its own port
		}
		port := *p.Port
		if cfg.Settings.EnforceUniquePorts {
			if err := ValidateUniquePorts(cfg, k, port); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
		if PortInUse(port) {
			return fmt.Errorf("%s: port %d already in use", k, port)
		}
	}
	return nil
}