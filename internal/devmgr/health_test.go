package devmgr

import (
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// writePidFor writes a pid file for key inside a temp pidDir.
func writePidFor(t *testing.T, pidDir, key string, pid int) {
	t.Helper()
	pp, err := PidFile(pidDir, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pp, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// freeLocalPort reserves an ephemeral port and releases it, so the caller can
// rely on it being unused (and refused) during the test.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind ephemeral port")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestWaitReadyPortAccepts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind ephemeral port")
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	pidDir := t.TempDir()
	writePidFor(t, pidDir, "web", os.Getpid())
	if st := WaitReady(pidDir, "web", &port, 2*time.Second); st != Ready {
		t.Fatalf("WaitReady = %v, want Ready", st)
	}
}

func TestWaitReadyStillStarting(t *testing.T) {
	port := freeLocalPort(t)
	pidDir := t.TempDir()
	writePidFor(t, pidDir, "web", os.Getpid())
	st := WaitReady(pidDir, "web", &port, 400*time.Millisecond)
	if st != StillStarting {
		t.Fatalf("WaitReady = %v, want StillStarting", st)
	}
}

func TestWaitReadyDied(t *testing.T) {
	proc, err := os.StartProcess("/bin/true", []string{"true"}, &os.ProcAttr{})
	if err != nil {
		t.Skip("cannot spawn /bin/true")
	}
	if _, err = proc.Wait(); err != nil {
		t.Fatal(err)
	}
	pidDir := t.TempDir()
	writePidFor(t, pidDir, "ghost", proc.Pid)
	port := freeLocalPort(t)
	if st := WaitReady(pidDir, "ghost", &port, 2*time.Second); st != Died {
		t.Fatalf("WaitReady = %v, want Died", st)
	}
}

func TestWaitReadyAliveNoPort(t *testing.T) {
	pidDir := t.TempDir()
	writePidFor(t, pidDir, "worker", os.Getpid())
	start := time.Now()
	st := WaitReady(pidDir, "worker", nil, 10*time.Second)
	if st != AliveNoPort {
		t.Fatalf("WaitReady = %v, want AliveNoPort", st)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("portless wait took %s; grace window not applied", elapsed)
	}
}

func TestPortAccepting(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind ephemeral port")
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if !portAccepting(port) {
		t.Fatal("portAccepting = false with live listener")
	}
	if portAccepting(freeLocalPort(t)) {
		t.Fatal("portAccepting = true without listener")
	}
}
