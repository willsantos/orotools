package delivery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOfficialGenerationAgainstRealStandardsCheckout runs the whole M05.4
// pipeline against a REAL standards checkout. It is env-gated and skipped in
// normal runs: set OROBORUS_STANDARDS_ROOT to the root of a clean Git checkout
// of oroborus-engineering-standards to enable it, e.g.
//
//	OROBORUS_STANDARDS_ROOT=/tmp/opencode/w1-standards go test ./internal/delivery/ -run TestOfficialGenerationAgainstRealStandardsCheckout -v
//
// The revision is read with plain git in the TEST (the delivery package never
// runs git); cleanliness is assumed to be the operator's responsibility here,
// and LoadOfficialSource still refuses symlinks or missing inputs.
func TestOfficialGenerationAgainstRealStandardsCheckout(t *testing.T) {
	standardsRoot := os.Getenv("OROBORUS_STANDARDS_ROOT")
	if standardsRoot == "" {
		t.Skip("OROBORUS_STANDARDS_ROOT not set; skipping real-standards integration")
	}
	cmd := exec.Command("git", "-C", standardsRoot, "rev-parse", "HEAD")
	cmd.Env = fixtureGitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	revision := strings.TrimSpace(string(out))
	source, err := LoadOfficialSource(standardsRoot, revision)
	if err != nil {
		t.Fatalf("real standards checkout refused: %v", err)
	}
	if source.TemplateID != OfficialTemplateID {
		t.Fatalf("template id = %q, want %q", source.TemplateID, OfficialTemplateID)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module probe\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanGeneration(project, source, planOptions("integration-probe"))
	if err != nil {
		t.Fatalf("generation against real distribution failed: %v", err)
	}
	if plan.Pin.DistributionVersion == "" || plan.Pin.TemplatePath == "" {
		t.Fatalf("incomplete pin: %+v", plan.Pin)
	}
	t.Logf("pin: distributionVersion=%s template=%s revision=%.12s", plan.Pin.DistributionVersion, plan.Pin.TemplatePath, plan.Pin.StandardsRevision)
}
