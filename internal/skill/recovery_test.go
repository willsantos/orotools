package skill_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

// b64 encodes payload bytes the way beginTransaction journals them.
func b64(t *testing.T, data []byte) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(data)
}

// journalJSON builds a .skills-transaction.json payload. Empty stage/backup
// are omitted, mirroring the omitempty tags of the real journal.
func journalJSON(t *testing.T, target, stage, backup string, hadManifest bool, oldManifest []byte, hadLock bool, oldLock []byte) []byte {
	t.Helper()
	doc := map[string]any{"target": target, "hadManifest": hadManifest, "hadLock": hadLock}
	if stage != "" {
		doc["stage"] = stage
	}
	if backup != "" {
		doc["backup"] = backup
	}
	if hadManifest {
		doc["oldManifest"] = b64(t, oldManifest)
	}
	if hadLock {
		doc["oldLock"] = b64(t, oldLock)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func writeJournal(t *testing.T, root string, payload []byte) string {
	t.Helper()
	path := filepath.Join(root, skill.LockDir, ".skills-transaction.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// deadChildPID spawns a real child process and returns its pid after it has
// exited — a verifiably dead pid, hermetically (the test binary itself,
// asked to run no tests).
func deadChildPID(t *testing.T) int {
	t.Helper()
	if os.Getenv("OROTOOLS_RECOVERY_CHILD") == "1" {
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=NoRecoveryChildTestMatchesHere", "-test.v=false")
	cmd.Env = append(os.Environ(), "OROTOOLS_RECOVERY_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait() // the child exits immediately; pid is now verifiably dead
	return pid
}

func writeOwnerLock(t *testing.T, root string, pid int) string {
	t.Helper()
	lockDir := filepath.Join(root, skill.LockDir, ".skills-operation.lock")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	owner, err := json.Marshal(map[string]any{"token": "fixture-token", "pid": pid})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), owner, 0o600); err != nil {
		t.Fatal(err)
	}
	return lockDir
}

func TestInspectOperationLockStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		root := t.TempDir()
		info, err := skill.InspectOperationLock(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.Present {
			t.Fatalf("info = %+v", info)
		}
	})

	t.Run("live owner pid", func(t *testing.T) {
		root := t.TempDir()
		writeOwnerLock(t, root, os.Getpid())
		info, err := skill.InspectOperationLock(root)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Present || !info.IsDir || !info.PIDValid {
			t.Fatalf("info = %+v", info)
		}
		if info.Liveness != "alive" && info.Liveness != "cannot-verify" {
			t.Fatalf("liveness = %q para o próprio processo de teste", info.Liveness)
		}
	})

	t.Run("dead owner pid", func(t *testing.T) {
		root := t.TempDir()
		writeOwnerLock(t, root, deadChildPID(t))
		info, err := skill.InspectOperationLock(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.Liveness != "dead" && info.Liveness != "cannot-verify" {
			t.Fatalf("liveness = %q, want dead (ou cannot-verify fora do unix)", info.Liveness)
		}
	})

	t.Run("corrupt owner marker", func(t *testing.T) {
		root := t.TempDir()
		lockDir := writeOwnerLock(t, root, os.Getpid())
		if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := skill.InspectOperationLock(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.ParseErr == "" {
			t.Fatal("owner.json inválido deveria sinalizar ParseErr (fail-closed)")
		}
	})

	t.Run("missing owner marker", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, skill.LockDir, ".skills-operation.lock"), 0o700); err != nil {
			t.Fatal(err)
		}
		info, err := skill.InspectOperationLock(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.ParseErr == "" {
			t.Fatal("owner.json ausente deveria sinalizar ParseErr")
		}
	})

	t.Run("lock path is not a directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, skill.LockDir, ".skills-operation.lock"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := skill.InspectOperationLock(root)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Present || info.IsDir {
			t.Fatalf("info = %+v", info)
		}
	})
}

func TestReleaseStaleProjectLockVerifiesLiveness(t *testing.T) {
	t.Run("absent lock is an idempotent success", func(t *testing.T) {
		root := t.TempDir()
		info, err := skill.ReleaseStaleProjectLock(root, false)
		if err != nil {
			t.Fatal(err)
		}
		if info.Present {
			t.Fatalf("info = %+v", info)
		}
	})

	t.Run("verifiably dead pid is released", func(t *testing.T) {
		root := t.TempDir()
		lockDir := writeOwnerLock(t, root, deadChildPID(t))
		info, err := skill.ReleaseStaleProjectLock(root, false)
		if errors.Is(err, skill.ErrProjectLocked) {
			t.Fatalf("dono morto recusado: %v", err)
		}
		if err != nil && info.Liveness == "cannot-verify" {
			t.Skip("plataforma não verifica pids; exigiria --force")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
			t.Fatal("lock não foi removido")
		}
	})

	t.Run("live pid is refused and lock preserved", func(t *testing.T) {
		root := t.TempDir()
		lockDir := writeOwnerLock(t, root, os.Getpid())
		_, err := skill.ReleaseStaleProjectLock(root, false)
		if err == nil {
			t.Fatal("dono vivo deveria ser recusado")
		}
		if !errors.Is(err, skill.ErrProjectLocked) {
			t.Fatalf("erro = %v, want ErrProjectLocked", err)
		}
		if _, err := os.Stat(lockDir); err != nil {
			t.Fatal("lock de dono vivo não deveria ser removido")
		}
	})

	t.Run("corrupt owner marker is refused", func(t *testing.T) {
		root := t.TempDir()
		lockDir := writeOwnerLock(t, root, os.Getpid())
		if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte("garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := skill.ReleaseStaleProjectLock(root, false); err == nil {
			t.Fatal("owner.json ilegível deveria falhar fechado")
		}
		if _, err := os.Stat(lockDir); err != nil {
			t.Fatal("lock não deveria ser removido sem verificação")
		}
	})

	t.Run("invalid pid is refused", func(t *testing.T) {
		root := t.TempDir()
		lockDir := writeOwnerLock(t, root, 0)
		if _, err := skill.ReleaseStaleProjectLock(root, false); err == nil {
			t.Fatal("pid inválido deveria ser recusado")
		}
		if _, err := os.Stat(lockDir); err != nil {
			t.Fatal("lock não deveria ser removido")
		}
	})

	t.Run("non-directory lock path is refused", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, skill.LockDir, ".skills-operation.lock")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := skill.ReleaseStaleProjectLock(root, false); err == nil {
			t.Fatal("caminho que não é diretório deveria ser recusado")
		}
	})
}

func TestInspectTransactionStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		root := t.TempDir()
		info, err := skill.InspectTransaction(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.Present || !info.PayloadOK {
			t.Fatalf("info = %+v", info)
		}
	})

	t.Run("valid journal decodes payload sizes", func(t *testing.T) {
		root := t.TempDir()
		manifest := []byte("manifest-v1\n")
		lock := []byte("lock-v1\n")
		writeJournal(t, root, journalJSON(t, ".opencode/skills/foo", "", "", true, manifest, true, lock))
		info, err := skill.InspectTransaction(root)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Present || !info.PayloadOK || info.Problem != "" {
			t.Fatalf("info = %+v", info)
		}
		if info.Target != ".opencode/skills/foo" || !info.HadManifest || !info.HadLock {
			t.Fatalf("info = %+v", info)
		}
		if info.OldManifestBytes != len(manifest) || info.OldLockBytes != len(lock) {
			t.Fatalf("tamanhos = %d/%d, want %d/%d", info.OldManifestBytes, info.OldLockBytes, len(manifest), len(lock))
		}
	})

	t.Run("corrupt base64 payload is flagged", func(t *testing.T) {
		root := t.TempDir()
		writeJournal(t, root, []byte(`{"target":".opencode/skills/foo","hadLock":true,"oldLock":"@@@ nao base64 @@@"}`))
		info, err := skill.InspectTransaction(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.PayloadOK || info.Problem == "" {
			t.Fatalf("info = %+v", info)
		}
	})

	t.Run("journal without any payload is flagged", func(t *testing.T) {
		root := t.TempDir()
		writeJournal(t, root, []byte(`{"target":".opencode/skills/foo","hadManifest":false,"hadLock":false}`))
		info, err := skill.InspectTransaction(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.PayloadOK || info.Problem == "" {
			t.Fatalf("info = %+v", info)
		}
	})

	t.Run("unreadable journal is flagged", func(t *testing.T) {
		root := t.TempDir()
		writeJournal(t, root, []byte("[[["))
		info, err := skill.InspectTransaction(root)
		if err != nil {
			t.Fatal(err)
		}
		if info.PayloadOK || info.Problem == "" {
			t.Fatalf("info = %+v", info)
		}
	})
}

// rollbackFixture mounts a project mid-replace: the target holds NEW content,
// the backup holds the OLD content, the stage is owned, and the journal
// records the pre-operation manifest and lock bytes.
type rollbackFixture struct {
	root         string
	manifestV1   []byte
	lockV1       []byte
	targetRel    string
	stageAbs     string
	backupAbs    string
	manifestPath string
	lockPath     string
	journalPath  string
}

func newRollbackFixture(t *testing.T) rollbackFixture {
	t.Helper()
	root := t.TempDir()
	f := rollbackFixture{
		root:         root,
		manifestV1:   []byte("version: 1\n# manifest anterior\n"),
		lockV1:       []byte("# lock anterior\nversion: 1\nskills: []\n"),
		targetRel:    ".opencode/skills/foo",
		manifestPath: filepath.Join(root, "orotools.yaml"),
		lockPath:     filepath.Join(root, skill.LockDir, skill.LockFilename),
	}
	if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-operation state files.
	if err := os.WriteFile(f.manifestPath, f.manifestV1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.lockPath, f.lockV1, 0o644); err != nil {
		t.Fatal(err)
	}
	// Backup holds the OLD tree; the target was swapped to NEW content.
	f.backupAbs = filepath.Join(root, ".opencode", "skills", ".orotools-backup-local-foo-123")
	oldTree := filepath.Join(f.backupAbs, "references")
	if err := os.MkdirAll(oldTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.backupAbs, "SKILL.md"), []byte("---\nname: foo\ndescription: antiga\nmetadata:\n  version: \"1.0.0\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldTree, "a.md"), []byte("conteúdo antigo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newTree := filepath.Join(root, filepath.FromSlash(f.targetRel), "references")
	if err := os.MkdirAll(newTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(f.targetRel), "SKILL.md"), []byte("---\nname: foo\ndescription: nova\nmetadata:\n  version: \"2.0.0\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newTree, "a.md"), []byte("conteúdo novo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Owned stage left behind by the interrupted operation.
	f.stageAbs = filepath.Join(root, ".opencode", "skills", ".orotools-stage-local-foo-456")
	if err := os.MkdirAll(f.stageAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.stageAbs, ".orotools-owner.json"), []byte(`{"skill":"local:unknown/foo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.journalPath = writeJournal(t, root, journalJSON(t, f.targetRel, f.stageAbs, f.backupAbs, true, f.manifestV1, true, f.lockV1))
	return f
}

func assertTreeBytes(t *testing.T, dir, wantFile, wantBody string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, wantFile))
	if err != nil {
		t.Fatalf("ler %s em %s: %v", wantFile, dir, err)
	}
	if string(got) != wantBody {
		t.Fatalf("bytes de %s divergem: %q != %q", wantFile, got, wantBody)
	}
}

func TestRollbackTransactionFromJournalRestoresExactPriorState(t *testing.T) {
	f := newRollbackFixture(t)
	result, err := skill.RollbackTransactionFromJournal(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if result.NoOp {
		t.Fatalf("result = %+v", result)
	}
	if !result.RestoredTarget || !result.RestoredManifest || !result.RestoredLock || !result.RemovedStage {
		t.Fatalf("result = %+v", result)
	}
	// The target is byte-for-byte the pre-operation tree.
	assertTreeBytes(t, filepath.Join(f.root, filepath.FromSlash(f.targetRel)), "references/a.md", "conteúdo antigo\n")
	if _, err := os.Stat(f.backupAbs); !os.IsNotExist(err) {
		t.Fatal("backup deveria ter sido movido de volta para o alvo")
	}
	// Manifest and lock are the exact journaled bytes.
	gotManifest, err := os.ReadFile(f.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotManifest) != string(f.manifestV1) {
		t.Fatalf("manifest restaurado diverge:\n%q\n%q", gotManifest, f.manifestV1)
	}
	gotLock, err := os.ReadFile(f.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotLock) != string(f.lockV1) {
		t.Fatalf("lock restaurado diverge:\n%q\n%q", gotLock, f.lockV1)
	}
	if _, err := os.Stat(f.stageAbs); !os.IsNotExist(err) {
		t.Fatal("stage próprio deveria ser removido")
	}
	if _, err := os.Stat(f.journalPath); !os.IsNotExist(err) {
		t.Fatal("journal deveria ser removido ao final")
	}
}

func TestRollbackTransactionInstallFlowRemovesUnbackedTarget(t *testing.T) {
	root := t.TempDir()
	manifestV1 := []byte("version: 1\n")
	manifestPath := filepath.Join(root, "orotools.yaml")
	lockPath := filepath.Join(root, skill.LockDir, skill.LockFilename)
	if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestV1, 0o644); err != nil {
		t.Fatal(err)
	}
	// Install flow: no manifest before? Manifest existed, lock did NOT; the
	// transaction created both. Target is the fresh install, stage owned.
	targetRel := ".codex/skills/bar"
	newTree := filepath.Join(root, ".codex", "skills", "bar")
	if err := os.MkdirAll(newTree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newTree, "SKILL.md"), []byte("novo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("lock criado pela transação\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageAbs := filepath.Join(root, ".codex", "skills", ".orotools-stage-local-bar-1")
	if err := os.MkdirAll(stageAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageAbs, ".orotools-owner.json"), []byte(`{"skill":"local:unknown/bar"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	journalPath := writeJournal(t, root, journalJSON(t, targetRel, stageAbs, "", true, manifestV1, false, nil))

	result, err := skill.RollbackTransactionFromJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RemovedTarget || !result.RestoredManifest || result.RestoredLock || !result.RemovedStage {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(newTree); !os.IsNotExist(err) {
		t.Fatal("alvo de instalação interrompida deveria ser removido")
	}
	got, _ := os.ReadFile(manifestPath)
	if string(got) != string(manifestV1) {
		t.Fatalf("manifest restaurado diverge: %q", got)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("lock criado pela transação deveria ser removido (hadLock=false)")
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatal("journal deveria ser removido")
	}
}

func TestRollbackWithoutJournalIsNoOp(t *testing.T) {
	root := t.TempDir()
	result, err := skill.RollbackTransactionFromJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoOp {
		t.Fatalf("result = %+v", result)
	}
}

func TestRollbackCorruptPayloadFailsClosed(t *testing.T) {
	for name, payload := range map[string][]byte{
		"corrupt lock payload":     []byte(`{"target":".opencode/skills/foo","hadLock":true,"oldLock":"@@@"}`),
		"empty lock payload":       []byte(`{"target":".opencode/skills/foo","hadLock":true,"oldLock":""}`),
		"corrupt manifest payload": []byte(`{"target":".opencode/skills/foo","hadManifest":true,"oldManifest":"###"}`),
		"no payloads at all":       []byte(`{"target":".opencode/skills/foo","hadManifest":false,"hadLock":false}`),
	} {
		t.Run(name, func(t *testing.T) {
			f := newRollbackFixture(t)
			// Replace the journal with the broken payload.
			if err := os.WriteFile(f.journalPath, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			beforeTarget, _ := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.targetRel), "SKILL.md"))
			beforeManifest, _ := os.ReadFile(f.manifestPath)
			beforeLock, _ := os.ReadFile(f.lockPath)

			_, err := skill.RollbackTransactionFromJournal(f.root)
			if err == nil {
				t.Fatal("rollback deveria falhar fechado")
			}
			if !strings.Contains(err.Error(), "fail-closed") && !strings.Contains(err.Error(), "preservado") {
				t.Fatalf("erro = %v", err)
			}
			afterTarget, _ := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.targetRel), "SKILL.md"))
			afterManifest, _ := os.ReadFile(f.manifestPath)
			afterLock, _ := os.ReadFile(f.lockPath)
			if string(beforeTarget) != string(afterTarget) || string(beforeManifest) != string(afterManifest) || string(beforeLock) != string(afterLock) {
				t.Fatal("estado mutado num rollback recusado")
			}
			if _, statErr := os.Stat(f.journalPath); statErr != nil {
				t.Fatal("journal deveria ser preservado para inspeção")
			}
		})
	}
}

func TestRollbackMissingBackupFailsClosed(t *testing.T) {
	f := newRollbackFixture(t)
	if err := os.RemoveAll(f.backupAbs); err != nil {
		t.Fatal(err)
	}
	beforeManifest, _ := os.ReadFile(f.manifestPath)
	beforeLock, _ := os.ReadFile(f.lockPath)

	if _, err := skill.RollbackTransactionFromJournal(f.root); err == nil {
		t.Fatal("backup desaparecido deveria falhar fechado")
	}
	afterManifest, _ := os.ReadFile(f.manifestPath)
	afterLock, _ := os.ReadFile(f.lockPath)
	if string(beforeManifest) != string(afterManifest) || string(beforeLock) != string(afterLock) {
		t.Fatal("estado mutado num rollback recusado")
	}
	if target, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.targetRel), "SKILL.md")); err != nil || !strings.Contains(string(target), "nova") {
		t.Fatal("alvo não deveria ser tocado num rollback recusado")
	}
	if _, err := os.Stat(f.journalPath); err != nil {
		t.Fatal("journal deveria ser preservado")
	}
}

func TestRollbackRefusesTamperedJournalPaths(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{"state dir", ".orotools"},
		{"project root", "."},
		{"traversal", "../../etc"},
		{"absolute", "/etc"},
		{"manifest file", "orotools.yaml"},
		{"agent dir itself", ".opencode"},
		{"skills dir itself", ".opencode/skills"},
		{"nested beyond one segment", ".opencode/skills/foo/sub"},
		{"unknown agent dir", ".windsurf/skills/foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
				t.Fatal(err)
			}
			manifestV1 := []byte("version: 1\n")
			manifestPath := filepath.Join(root, "orotools.yaml")
			if err := os.WriteFile(manifestPath, manifestV1, 0o644); err != nil {
				t.Fatal(err)
			}
			journalPath := writeJournal(t, root, journalJSON(t, tt.target, "", "", true, manifestV1, true, []byte("lock\n")))
			if _, err := skill.RollbackTransactionFromJournal(root); err == nil {
				t.Fatalf("journal com alvo %q deveria ser recusado", tt.target)
			}
			if _, err := os.Stat(journalPath); err != nil {
				t.Fatal("journal deveria ser preservado")
			}
			if got, err := os.ReadFile(manifestPath); err != nil || string(got) != string(manifestV1) {
				t.Fatal("manifest não deveria ser tocado")
			}
		})
	}
}

func TestRollbackRefusesTamperedStageAndBackupPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestV1 := []byte("version: 1\n")
	manifestPath := filepath.Join(root, "orotools.yaml")
	if err := os.WriteFile(manifestPath, manifestV1, 0o644); err != nil {
		t.Fatal(err)
	}
	targetRel := ".opencode/skills/foo"
	targetAbs := filepath.Join(root, ".opencode", "skills", "foo")
	if err := os.MkdirAll(targetAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		fields func() string
	}{
		{"stage sem prefixo interno", func() string { return filepath.Join(root, ".opencode", "skills", "outro") }},
		{"stage fora da raiz", func() string { return filepath.Join(root, "..", "fora") }},
		{"stage relativo", func() string { return ".opencode/skills/.orotools-stage-x" }},
		{"backup sem prefixo interno", func() string { return filepath.Join(root, ".opencode", "skills", "nao-backup") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := journalJSON(t, targetRel, "", "", true, manifestV1, true, []byte("lock\n"))
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(tt.name, "stage") {
				doc["stage"] = tt.fields()
			} else {
				doc["backup"] = tt.fields()
			}
			out, _ := json.Marshal(doc)
			journalPath := writeJournal(t, root, out)
			if _, err := skill.RollbackTransactionFromJournal(root); err == nil {
				t.Fatal("caminho interno fora do contrato deveria ser recusado")
			}
			if _, err := os.Stat(journalPath); err != nil {
				t.Fatal("journal deveria ser preservado")
			}
			if _, err := os.Stat(targetAbs); err != nil {
				t.Fatal("alvo não deveria ser tocado")
			}
		})
	}
}

func TestRollbackLeavesUnownedStageInPlace(t *testing.T) {
	f := newRollbackFixture(t)
	if err := os.Remove(filepath.Join(f.stageAbs, ".orotools-owner.json")); err != nil {
		t.Fatal(err)
	}
	result, err := skill.RollbackTransactionFromJournal(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedStage {
		t.Fatal("stage sem marcador de propriedade não deveria ser removido")
	}
	if len(result.LeftOvers) != 1 || !strings.Contains(result.LeftOvers[0], ".orotools-stage-") {
		t.Fatalf("leftovers = %v", result.LeftOvers)
	}
	if _, err := os.Stat(f.stageAbs); err != nil {
		t.Fatal("stage sem dono deveria ficar no lugar para revisão")
	}
	if _, err := os.Stat(f.journalPath); !os.IsNotExist(err) {
		t.Fatal("journal deveria ser removido mesmo com leftover")
	}
}
