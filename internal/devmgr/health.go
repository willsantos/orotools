package devmgr

import (
	"net"
	"strconv"
	"time"
)

// ReadyState is the post-start readiness outcome of a project (FR-22).
type ReadyState int

const (
	// Ready means the process is alive and its port accepts connections.
	Ready ReadyState = iota
	// AliveNoPort means the process is alive but has no port to probe.
	AliveNoPort
	// StillStarting means the process is alive but the port did not open
	// before the timeout — a slow boot, not a failure.
	StillStarting
	// Died means the process exited during the wait window.
	Died
)

const (
	readyPollInterval = 250 * time.Millisecond
	readyDialTimeout  = 250 * time.Millisecond
	// noPortGrace is the liveness window for portless projects: surviving it
	// is the best "it started" signal available without a port to dial.
	noPortGrace = 2 * time.Second
)

// WaitReady polls a freshly started project until it is demonstrably up,
// still starting, or dead (FR-22). With a configured port, readiness is a
// successful TCP dial to localhost; without one, it is surviving the grace
// window. The deadline + probe + sleep loop follows the stopGroup pattern.
func WaitReady(pidDir, key string, port *int, timeout time.Duration) ReadyState {
	wait := timeout
	if wait < readyPollInterval {
		wait = readyPollInterval
	}
	if port == nil && wait > noPortGrace {
		wait = noPortGrace
	}
	deadline := time.Now().Add(wait)
	for {
		if !IsRunning(pidDir, key) {
			return Died
		}
		if port != nil && portAccepting(*port) {
			return Ready
		}
		if !time.Now().Before(deadline) {
			if port == nil {
				return AliveNoPort
			}
			return StillStarting
		}
		time.Sleep(readyPollInterval)
	}
}

// portAccepting reports whether localhost:port accepts TCP connections.
// Unlike PortInUse (a bind probe), this verifies someone is listening and
// accepting — the signal a dev server is actually serving.
func portAccepting(port int) bool {
	c, err := net.DialTimeout("tcp", "localhost:"+strconv.Itoa(port), readyDialTimeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
