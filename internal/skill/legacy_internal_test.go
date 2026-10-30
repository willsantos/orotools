package skill

// Internal tests for the migration and recovery seams that need the
// unexported lock-timeout knobs (kept small so live-holder refusals stay
// fast) and a direct vector test of the recovered legacy hash algorithm.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func contextWithTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// writeLegacyFixtureSkill mounts an installed skill tree under agentDir.
func writeLegacyFixtureSkill(t *testing.T, root, agentDir, name, version string) {
	t.Helper()
	dir := filepath.Join(root, agentDir, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMd := fmt.Sprintf("---\nname: %s\ndescription: skill %s\nmetadata:\n  version: %q\n---\n\ncorpo\n", name, name, version)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMd), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyContentHashMatchesRecoveredPythonSpec(t *testing.T) {
	// installer.py content_hash (discovery.py): sha256 over sorted
	// "relpath NUL bytes NUL" pairs, ignoring .skill-meta.json. Vector built
	// by hand: files "a.txt" ("um"), "sub/b.txt" ("dois") and the ignored
	// meta file — hash input is exactly:
	//   "a.txt\0um\0sub/b.txt\0dois\0"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("um"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("dois"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LegacyMetaFilename), []byte("ignorado"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256Of([]byte("a.txt\x00um\x00sub/b.txt\x00dois\x00"))
	got, err := legacyContentHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != sum {
		t.Fatalf("legacyContentHash = %s, want %s", got, sum)
	}
}

func TestApplyLegacyMigrationMutualExclusion(t *testing.T) {
	old := projectAcquireTimeout
	projectAcquireTimeout = 150 * time.Millisecond
	defer func() { projectAcquireTimeout = old }()

	root := t.TempDir()
	writeLegacyFixtureSkill(t, root, ".opencode", "alpha", "1.0.0")
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".opencode", "skills", "alpha")
	hash, err := legacyContentHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	lockJSON := fmt.Sprintf(`{"version":2,"skills":{"alpha":{"name":"alpha","source":"local","contentHash":%q,"installedAt":"2026-04-07T00:09:56Z","updatedAt":"2026-04-07T00:09:56Z","agents":["opencode"],"method":"copy","global":false}}}`, hash)
	if err := os.WriteFile(filepath.Join(root, LegacyLockRelPath), []byte(lockJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	projectLock, err := AcquireProjectLock(contextWithTimeout(t, 2*time.Second), root)
	if err != nil {
		t.Fatal(err)
	}
	defer projectLock.Release()

	if _, err := ApplyLegacyMigration(root, nil); err == nil {
		t.Fatal("apply deveria respeitar o lock de processo de outro dono vivo")
	} else if !strings.Contains(err.Error(), "project lock") {
		t.Fatalf("erro = %v", err)
	}
}

func TestRollbackTransactionRefusesLiveProjectLockHolder(t *testing.T) {
	old := projectLockTimeout
	projectLockTimeout = 150 * time.Millisecond
	defer func() { projectLockTimeout = old }()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(root, LockDir, transactionFilename)
	journal := `{"target":".opencode/skills/foo","hadManifest":false,"hadLock":false}`
	if err := os.WriteFile(journalPath, []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	projectLock, err := AcquireProjectLock(contextWithTimeout(t, 2*time.Second), root)
	if err != nil {
		t.Fatal(err)
	}
	defer projectLock.Release()

	if _, err := RollbackTransactionFromJournal(root); err == nil {
		t.Fatal("rollback deveria recusar dono vivo do lock de processo")
	} else if !strings.Contains(err.Error(), "project lock") {
		t.Fatalf("erro = %v", err)
	}
	if _, statErr := os.Stat(journalPath); statErr != nil {
		t.Fatal("journal deveria ser preservado quando o rollback é recusado")
	}
}
