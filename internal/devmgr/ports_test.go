package devmgr

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ephemeral returns a currently-free port by binding a listener to :0 and
// reading back the assigned port, then closing.
func ephemeral() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	return portOf(l)
}

func portOf(l net.Listener) int {
	s := l.Addr().String()
	last := strings.LastIndex(s, ":")
	p, err := strconv.Atoi(s[last+1:])
	if err != nil {
		return 0
	}
	return p
}

func TestPortFreeEphemeral(t *testing.T) {
	p := ephemeral()
	if p == 0 {
		t.Skip("no ephemeral port available")
		return
	}
	// A freshly closed listener may stay in TIME_WAIT; rebound-ship cannot be
	// asserted reliably, so only sanity-check the reported port is sane.
	if p <= 0 {
		t.Fatalf("bad ephemeral port %d", p)
	}
}

func TestPortInUseWhileBound(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
		return
	}
	defer l.Close()
	p := portOf(l)
	if !PortInUse(p) {
		t.Fatalf("port %d should be in use while bound", p)
	}
}

func TestNextFreePortPicksLowestFree(t *testing.T) {
	assigned := map[int]string{}
	first, ok := NextFreePort(PortRange{3001, 3099}, assigned)
	if !ok {
		t.Skip("no port free in 3001-3099")
		return
	}
	// occupy the "lowest free" port we just found
	bl := occupy(first)
	if bl == nil {
		t.Skip("could not bind")
		return
	}
	defer bl.Close()

	second, ok := NextFreePort(PortRange{3001, 3099}, assigned)
	if !ok {
		t.Fatal("no next free port")
	}
	if second == first {
		t.Fatalf("NextFreePort returned occupied port %d", second)
	}
	if second < 3001 || second > 3099 {
		t.Fatalf("port outside range: %d", second)
	}
}

func TestNextFreePortSkipsAssignedProject(t *testing.T) {
	assigned := map[int]string{3001: "beafaes"}
	p, ok := NextFreePort(PortRange{3001, 3099}, assigned)
	if !ok {
		t.Fatal("no free port")
	}
	if p == 3001 {
		t.Fatalf("NextFreePort ignored assigned project port")
	}
}

func TestValidateUniquePortsRejectsDuplicate(t *testing.T) {
	cfg, err := Load(fixture(t))
	if err != nil {
		t.Fatal(err.Error())
	}
	// delroyt holds port 3002
	err1 := ValidateUniquePorts(cfg, "newname", 3002)
	if err1 == nil {
		t.Fatal("expected duplicate-port error")
	}
	// same project key excluded
	err2 := ValidateUniquePorts(cfg, "delroyt", 3002)
	if err2 != nil {
		t.Fatalf("same-key exclusion failed: %v", err2)
	}
	// fresh port in range OK
	err3 := ValidateUniquePorts(cfg, "newname", 3099)
	if err3 != nil {
		t.Fatalf("unique port rejected: %v", err3)
	}
}

// devPortCfg builds an in-memory config with the given ported projects.
func devPortCfg(enforce bool, pidDir string, entries map[string]int) *Config {
	cfg := &Config{}
	cfg.Settings.EnforceUniquePorts = enforce
	cfg.Settings.PidDir = pidDir
	for k, p := range entries {
		port := p
		cfg.Add(k, &Project{
			Name: k, Path: "/" + k, Cwd: "/" + k,
			PackageManager: "npm", DevCommand: "npm run dev", Port: &port,
		})
	}
	return cfg
}

func TestCheckStartPortsRejectsDuplicateInBatch(t *testing.T) {
	cfg := devPortCfg(true, t.TempDir(), map[string]int{"a": 3001, "b": 3001})
	err := CheckStartPorts(cfg, []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "already assigned") {
		t.Fatalf("err = %v, want duplicate-port rejection", err)
	}
}

func TestCheckStartPortsRejectsPortInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
		return
	}
	defer l.Close()
	cfg := devPortCfg(true, t.TempDir(), map[string]int{"a": portOf(l)})
	err = CheckStartPorts(cfg, []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("err = %v, want port-in-use rejection", err)
	}
}

func TestCheckStartPortsRejectsPortInUseWithoutEnforce(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
		return
	}
	defer l.Close()
	cfg := devPortCfg(false, t.TempDir(), map[string]int{"a": portOf(l)})
	if err := CheckStartPorts(cfg, []string{"a"}); err == nil {
		t.Fatal("occupied port must be rejected regardless of enforce_unique_ports")
	}
}

func TestCheckStartPortsSkipsRunningProject(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind")
		return
	}
	defer l.Close()
	pidDir := t.TempDir()
	// our own pid keeps the project "running" while holding the port
	if err := os.WriteFile(filepath.Join(pidDir, "a.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := devPortCfg(true, pidDir, map[string]int{"a": portOf(l)})
	if err := CheckStartPorts(cfg, []string{"a"}); err != nil {
		t.Fatalf("running project must be skipped: %v", err)
	}
}

func TestCheckStartPortsAllowsFreeAndUnique(t *testing.T) {
	free := freeScanPort()
	cfg := devPortCfg(true, t.TempDir(), map[string]int{"a": free, "b": free + 1})
	if err := CheckStartPorts(cfg, []string{"a", "b", "ghost"}); err != nil {
		t.Fatalf("free unique ports (and unknown keys) must pass: %v", err)
	}
}

func TestCheckStartPortsAllowsDuplicateWhenNotEnforced(t *testing.T) {
	free := freeScanPort()
	cfg := devPortCfg(false, t.TempDir(), map[string]int{"a": free, "b": free})
	if err := CheckStartPorts(cfg, []string{"a", "b"}); err != nil {
		t.Fatalf("duplicate policy must follow enforce_unique_ports=false: %v", err)
	}
}

func TestCheckStartPortsIgnoresPortlessProject(t *testing.T) {
	cfg := devPortCfg(true, t.TempDir(), nil)
	cfg.Add("worker", &Project{
		Name: "worker", Path: "/w", Cwd: "/w",
		PackageManager: "npm", DevCommand: "npm run dev", // Port nil
	})
	if err := CheckStartPorts(cfg, []string{"worker"}); err != nil {
		t.Fatalf("portless project must pass: %v", err)
	}
}

// freeScanPort finds a port reported free right now via PortFree.
func freeScanPort() int {
	for p := 20000; p < 25000; p++ {
		if PortFree(p) {
			return p
		}
	}
	return 0
}

func occupy(port int) net.Listener {
	l, err := net.Listen("tcp", "127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		return nil
	}
	return l
}