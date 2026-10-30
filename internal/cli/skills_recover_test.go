package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

func runRecover(t *testing.T, root string, mutate func(*skillsRecoverOptions)) (string, error) {
	t.Helper()
	opts := skillsRecoverOptions{
		manifestPath: filepath.Join(root, "orotools.yaml"),
		out:          &bytes.Buffer{},
		errOut:       &bytes.Buffer{},
	}
	if mutate != nil {
		mutate(&opts)
	}
	err := runSkillsRecover(opts)
	out := opts.out.(*bytes.Buffer).String()
	if errOut := opts.errOut.(*bytes.Buffer).String(); errOut != "" {
		t.Fatalf("stderr inesperado: %q", errOut)
	}
	return out, err
}

func writeRecoverOwnerLock(t *testing.T, root string, pid int) string {
	t.Helper()
	lockDir := filepath.Join(root, skill.LockDir, ".skills-operation.lock")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	owner, err := json.Marshal(map[string]any{"token": "cli-fixture", "pid": pid})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), owner, 0o600); err != nil {
		t.Fatal(err)
	}
	return lockDir
}

func recoverDeadChildPID(t *testing.T) int {
	t.Helper()
	if os.Getenv("OROTOOLS_RECOVER_CHILD") == "1" {
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=NoRecoverChildTestMatchesHere", "-test.v=false")
	cmd.Env = append(os.Environ(), "OROTOOLS_RECOVER_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	return pid
}

func TestRunSkillsRecoverDefaultInspectWritesNothing(t *testing.T) {
	root := t.TempDir()
	before := fsSnapshot(t, root)
	out, err := runRecover(t, root, nil)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	after := fsSnapshot(t, root)
	if len(before) != len(after) {
		t.Fatalf("inspeção escreveu arquivos: %d -> %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("conteúdo mudou na inspeção: %s", k)
		}
	}
	if !strings.Contains(out, "somente leitura") || !strings.Contains(out, "lock de processo: ausente") || !strings.Contains(out, "journal de transação: ausente") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestRunSkillsRecoverInspectShowsLockOwnerAndLiveness(t *testing.T) {
	root := t.TempDir()
	writeRecoverOwnerLock(t, root, os.Getpid())
	out, err := runRecover(t, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, fmt.Sprintf("pid %d", os.Getpid())) || !strings.Contains(out, "liveness") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestRunSkillsRecoverInspectFlagsCorruptJournalAndFails(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, skill.LockDir, ".skills-transaction.json"), []byte(`{"target":".opencode/skills/foo","hadLock":true,"oldLock":"@@@"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runRecover(t, root, nil)
	if err == nil {
		t.Fatalf("journal corrompido deveria sair com erro:\n%s", out)
	}
	if !strings.Contains(out, "PRESENTE") || !strings.Contains(out, "fail-closed") {
		t.Fatalf("output:\n%s", out)
	}
}

// recoverRollbackFixture mounts a project mid-replace for the CLI rollback.
func recoverRollbackFixture(t *testing.T) (root, manifestPath, lockPath, backupAbs, stageAbs, journalPath string, manifestV1, lockV1 []byte) {
	t.Helper()
	root = t.TempDir()
	manifestV1 = []byte("version: 1\n# anterior\n")
	lockV1 = []byte("# lock anterior\nversion: 1\nskills: []\n")
	manifestPath = filepath.Join(root, "orotools.yaml")
	lockPath = filepath.Join(root, skill.LockDir, skill.LockFilename)
	if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestV1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, lockV1, 0o644); err != nil {
		t.Fatal(err)
	}
	backupAbs = filepath.Join(root, ".opencode", "skills", ".orotools-backup-local-foo-1")
	if err := os.MkdirAll(backupAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupAbs, "SKILL.md"), []byte("antiga\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".opencode", "skills", "foo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("nova\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageAbs = filepath.Join(root, ".opencode", "skills", ".orotools-stage-local-foo-2")
	if err := os.MkdirAll(stageAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageAbs, ".orotools-owner.json"), []byte(`{"skill":"local:unknown/foo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	journal := map[string]any{
		"target":      ".opencode/skills/foo",
		"stage":       stageAbs,
		"backup":      backupAbs,
		"hadManifest": true,
		"oldManifest": base64.StdEncoding.EncodeToString(manifestV1),
		"hadLock":     true,
		"oldLock":     base64.StdEncoding.EncodeToString(lockV1),
	}
	raw, _ := json.Marshal(journal)
	journalPath = filepath.Join(root, skill.LockDir, ".skills-transaction.json")
	if err := os.WriteFile(journalPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, manifestPath, lockPath, backupAbs, stageAbs, journalPath, manifestV1, lockV1
}

func TestRunSkillsRecoverRollbackRestoresAndRemovesJournal(t *testing.T) {
	root, manifestPath, lockPath, backupAbs, stageAbs, journalPath, manifestV1, lockV1 := recoverRollbackFixture(t)
	out, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.rollback = true })
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	for _, want := range []string{"restaurado a partir do backup", "manifest anterior restaurado", "lock anterior restaurado", "journal removido"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output sem %q:\n%s", want, out)
		}
	}
	gotTarget, err := os.ReadFile(filepath.Join(root, ".opencode", "skills", "foo", "SKILL.md"))
	if err != nil || string(gotTarget) != "antiga\n" {
		t.Fatalf("alvo não voltou ao backup: %q", gotTarget)
	}
	if _, err := os.Stat(backupAbs); !os.IsNotExist(err) {
		t.Fatal("backup deveria ter virado o alvo")
	}
	gotManifest, _ := os.ReadFile(manifestPath)
	if string(gotManifest) != string(manifestV1) {
		t.Fatalf("manifest diverge: %q", gotManifest)
	}
	gotLock, _ := os.ReadFile(lockPath)
	if string(gotLock) != string(lockV1) {
		t.Fatalf("lock diverge: %q", gotLock)
	}
	if _, err := os.Stat(stageAbs); !os.IsNotExist(err) {
		t.Fatal("stage próprio deveria ser removido")
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatal("journal deveria ser removido")
	}
}

func TestRunSkillsRecoverRollbackWithoutJournalIsNoOp(t *testing.T) {
	root := t.TempDir()
	out, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.rollback = true })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nenhum journal") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestRunSkillsRecoverRollbackCorruptJournalFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(root, skill.LockDir, ".skills-transaction.json")
	if err := os.WriteFile(journalPath, []byte(`{"target":".opencode/skills/foo","hadLock":true,"oldLock":"@@@"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.rollback = true }); err == nil {
		t.Fatal("rollback com payload corrompido deveria falhar")
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatal("journal deveria ser preservado (fail-closed)")
	}
}

func TestRunSkillsRecoverReleaseStaleLockDeadPidReleased(t *testing.T) {
	root := t.TempDir()
	lockDir := writeRecoverOwnerLock(t, root, recoverDeadChildPID(t))
	out, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.releaseStale = true })
	if err != nil {
		if strings.Contains(err.Error(), "não consegue verificar") {
			t.Skip("plataforma sem verificação de pid")
		}
		t.Fatal(err)
	}
	if !strings.Contains(out, "liberado") {
		t.Fatalf("output:\n%s", out)
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatal("lock deveria ter sido removido")
	}
}

func TestRunSkillsRecoverReleaseStaleLockLivePidRefused(t *testing.T) {
	root := t.TempDir()
	lockDir := writeRecoverOwnerLock(t, root, os.Getpid())
	out, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.releaseStale = true })
	if err == nil {
		t.Fatalf("dono vivo deveria ser recusado:\n%s", out)
	}
	if !strings.Contains(err.Error(), "vivo") {
		t.Fatalf("erro = %v", err)
	}
	if _, statErr := os.Stat(lockDir); statErr != nil {
		t.Fatal("lock de dono vivo não deveria ser removido")
	}
}

func TestRunSkillsRecoverReleaseStaleLockAbsentIsSuccess(t *testing.T) {
	root := t.TempDir()
	out, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.releaseStale = true })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nenhum lock de processo") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestRunSkillsRecoverRejectsTwoActionsAtOnce(t *testing.T) {
	root := t.TempDir()
	if _, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.rollback = true; o.releaseStale = true }); err == nil {
		t.Fatal("duas ações deveriam ser rejeitadas")
	}
}

func TestRunSkillsRecoverRejectsReleaseWithForceOnLiveOwner(t *testing.T) {
	// --force autoriza apenas plataformas sem verificação de pid; num dono
	// vivo verificado, force não muda a recusa.
	root := t.TempDir()
	lockDir := writeRecoverOwnerLock(t, root, os.Getpid())
	_, err := runRecover(t, root, func(o *skillsRecoverOptions) { o.releaseStale = true; o.force = true })
	if err == nil {
		t.Fatal("dono vivo verificado deveria ser recusado mesmo com --force")
	}
	if _, statErr := os.Stat(lockDir); statErr != nil {
		t.Fatal("lock de dono vivo não deveria ser removido")
	}
}
