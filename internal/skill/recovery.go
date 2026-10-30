package skill

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"oroborus.dev/orotools/internal/manifest"
)

// Recovery surfaces for `oro skills recover`: the explicit, operator-driven
// path that complements the deliberately fail-closed engine. Automatic
// recovery inside the engine stays disabled (managed.go recoverTransaction);
// these helpers never run implicitly.

// OperationLockInfo is the inspection result for .orotools/.skills-operation.lock.
type OperationLockInfo struct {
	Path     string // absolute lock directory
	Present  bool
	IsDir    bool
	Token    string
	PID      int
	PIDValid bool
	ParseErr string // owner.json unreadable/malformed (fail-closed signal)
	Liveness string // "alive" | "dead" | "cannot-verify" | "invalid" | ""
}

// TransactionInfo is the inspection result for .orotools/.skills-transaction.json.
type TransactionInfo struct {
	Present          bool
	Path             string
	Target           string
	Stage            string
	Backup           string
	HadManifest      bool
	HadLock          bool
	OldManifestBytes int
	OldLockBytes     int
	PayloadOK        bool // base64 payloads decode when required
	Problem          string
}

// InspectOperationLock reads the process-lock owner marker without mutating
// anything and classifies the owner pid's liveness.
func InspectOperationLock(projectRoot string) (OperationLockInfo, error) {
	info := OperationLockInfo{}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return info, fmt.Errorf("resolve project root: %w", err)
	}
	lockDir, err := safeJoin(absRoot, filepath.ToSlash(filepath.Join(LockDir, operationLockName)))
	if err != nil {
		return info, fmt.Errorf("lock path: %w", err)
	}
	info.Path = lockDir
	stat, err := os.Lstat(lockDir)
	if os.IsNotExist(err) {
		return info, nil
	}
	if err != nil {
		return info, fmt.Errorf("inspect operation lock: %w", err)
	}
	info.Present = true
	info.IsDir = stat.IsDir()
	raw, err := os.ReadFile(filepath.Join(lockDir, "owner.json"))
	if err != nil {
		if os.IsNotExist(err) {
			info.ParseErr = "owner.json ausente: não é possível verificar o dono"
		} else {
			info.ParseErr = err.Error()
		}
		return info, nil
	}
	var owner lockOwner
	if err := json.Unmarshal(raw, &owner); err != nil {
		info.ParseErr = "owner.json inválido: " + err.Error()
		return info, nil
	}
	info.Token = owner.Token
	info.PID = owner.PID
	info.PIDValid = owner.PID > 0
	live, _ := processLiveness(owner.PID)
	info.Liveness = live
	return info, nil
}

// ReleaseStaleProjectLock removes .skills-operation.lock only when the owner
// pid is verifiably dead. A live owner, an unreadable marker, or a platform
// that cannot verify liveness (unless force is set) are refused — fail closed.
func ReleaseStaleProjectLock(projectRoot string, force bool) (OperationLockInfo, error) {
	info, err := InspectOperationLock(projectRoot)
	if err != nil {
		return info, err
	}
	if !info.Present {
		return info, nil // nothing to release: idempotent success
	}
	if !info.IsDir {
		return info, fmt.Errorf("release: %s não é um diretório de lock; inspecione manualmente", info.Path)
	}
	switch {
	case info.ParseErr != "":
		return info, fmt.Errorf("release: %s; remova manualmente somente após inspeção", info.ParseErr)
	case !info.PIDValid:
		return info, fmt.Errorf("release: owner.json sem pid válido; inspeção manual necessária")
	case info.Liveness == "alive":
		return info, fmt.Errorf("%w: owner pid %d está vivo; recusa liberar o lock de uma operação ativa", ErrProjectLocked, info.PID)
	case info.Liveness == "cannot-verify" && !force:
		return info, fmt.Errorf("release: plataforma não consegue verificar o pid %d; confirme manualmente e use --force", info.PID)
	case info.Liveness == "invalid":
		return info, fmt.Errorf("release: pid inválido no owner.json; inspeção manual necessária")
	}
	marker := filepath.Join(info.Path, "owner.json")
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return info, fmt.Errorf("release: remover owner.json: %w", err)
	}
	if err := os.Remove(info.Path); err != nil {
		return info, fmt.Errorf("release: remover lock: %w", err)
	}
	return info, nil
}

// InspectTransaction reads the operation journal without mutating anything.
func InspectTransaction(projectRoot string) (TransactionInfo, error) {
	info := TransactionInfo{PayloadOK: true}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return info, fmt.Errorf("resolve project root: %w", err)
	}
	journalPath := filepath.Join(absRoot, LockDir, transactionFilename)
	info.Path = journalPath
	raw, err := os.ReadFile(journalPath)
	if os.IsNotExist(err) {
		return info, nil
	}
	if err != nil {
		return info, fmt.Errorf("inspect transaction journal: %w", err)
	}
	info.Present = true
	var journal operationJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		info.PayloadOK = false
		info.Problem = "journal inválido: " + err.Error()
		return info, nil
	}
	info.Target, info.Stage, info.Backup = journal.Target, journal.Stage, journal.Backup
	info.HadManifest, info.HadLock = journal.HadManifest, journal.HadLock
	if journal.HadManifest {
		info.OldManifestBytes = base64Len(journal.OldManifest)
		if info.OldManifestBytes < 0 {
			info.PayloadOK = false
			info.Problem = "oldManifest não decodifica (base64)"
		}
	}
	if journal.HadLock {
		info.OldLockBytes = base64Len(journal.OldLock)
		if info.OldLockBytes < 0 {
			info.PayloadOK = false
			info.Problem = joinProblem(info.Problem, "oldLock não decodifica (base64)")
		}
	}
	if !journal.HadManifest && !journal.HadLock {
		info.PayloadOK = false
		info.Problem = joinProblem(info.Problem, "journal sem payload de manifest e de lock: estado anterior irrecuperável")
	}
	return info, nil
}

func base64Len(encoded string) int {
	if encoded == "" {
		return -1
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return -1
	}
	return len(decoded)
}

func joinProblem(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// projectLockTimeout bounds how long an explicit recovery waits for the
// project lock before refusing; tests shrink it to keep live-holder cases fast.
var projectLockTimeout = 10 * time.Second

// RollbackResult reports what one explicit rollback restored.
type RollbackResult struct {
	NoOp             bool
	RestoredTarget   bool // backup swapped back into the target
	RemovedTarget    bool // install-flow rollback: unowned new target removed
	RestoredManifest bool
	RestoredLock     bool
	RemovedStage     bool
	LeftOvers        []string // inert directories deliberately left for review
}

// RollbackTransactionFromJournal performs the explicit recovery the engine
// refuses to automate: restores the previous lock and/or manifest from the
// journal payloads, returns the target directory to its pre-operation state,
// removes owned staging, then removes the journal. Fail-closed rules:
//
//   - missing or undecodable payload when one is required → nothing is
//     mutated and the journal is preserved;
//   - a journaled backup that vanished from disk → nothing is mutated;
//   - stage directories without an ownership marker are left in place.
//
// It acquires the project lock when free and refuses a live holder.
func RollbackTransactionFromJournal(projectRoot string) (RollbackResult, error) {
	result := RollbackResult{}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return result, fmt.Errorf("resolve project root: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectLockTimeout)
	defer cancel()
	projectLock, err := AcquireProjectLock(ctx, absRoot)
	if err != nil {
		return result, fmt.Errorf("rollback: project lock: %w", err)
	}
	defer projectLock.Release()

	inspect, err := InspectTransaction(absRoot)
	if err != nil {
		return result, err
	}
	if !inspect.Present {
		result.NoOp = true
		return result, nil
	}
	if inspect.Problem != "" {
		return result, fmt.Errorf("rollback: recusado (fail-closed): %s; o journal foi preservado para inspeção em %s", inspect.Problem, inspect.Path)
	}

	raw, err := os.ReadFile(inspect.Path)
	if err != nil {
		return result, err
	}
	var journal operationJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return result, fmt.Errorf("rollback: journal ilegível: %w", err)
	}

	// Validate every path and payload BEFORE mutating anything. The journal
	// records Target as a project-relative path and Stage/Backup as absolute
	// internal-prefix paths (beginTransaction contract in managed.go). The
	// target must additionally be a skills destination (an agent skills dir
	// with one segment below it): a corrupt or tampered journal must never be
	// able to aim RemoveAll at arbitrary project paths like .orotools itself.
	if !validJournalTarget(journal.Target) {
		return result, fmt.Errorf("rollback: alvo do journal recusado: %q não é um diretório de destino de skills", journal.Target)
	}
	targetAbs, targetErr := safeJoin(absRoot, journal.Target)
	if targetErr != nil {
		return result, fmt.Errorf("rollback: alvo do journal recusado: %v", targetErr)
	}
	stageAbs := ""
	if journal.Stage != "" {
		if stageAbs, err = validateJournalInternalPath(absRoot, journal.Stage, stagePrefix); err != nil {
			return result, fmt.Errorf("rollback: stage do journal recusado: %w", err)
		}
	}
	backupAbs := ""
	if journal.Backup != "" {
		if backupAbs, err = validateJournalInternalPath(absRoot, journal.Backup, backupPrefix); err != nil {
			return result, fmt.Errorf("rollback: backup do journal recusado: %w", err)
		}
	}
	manifestAbs, err := safeJoin(absRoot, manifest.Filename)
	if err != nil {
		return result, fmt.Errorf("rollback: caminho do manifest recusado: %w", err)
	}
	lockAbs, err := safeJoin(absRoot, filepath.ToSlash(filepath.Join(LockDir, LockFilename)))
	if err != nil {
		return result, fmt.Errorf("rollback: caminho do lock recusado: %w", err)
	}
	var oldManifest, oldLock []byte
	if journal.HadManifest {
		if oldManifest, err = base64.StdEncoding.DecodeString(journal.OldManifest); err != nil || len(oldManifest) == 0 {
			return result, errors.New("rollback: recusado (fail-closed): payload do manifest anterior ausente ou inválido; journal preservado")
		}
	}
	if journal.HadLock {
		if oldLock, err = base64.StdEncoding.DecodeString(journal.OldLock); err != nil || len(oldLock) == 0 {
			return result, errors.New("rollback: recusado (fail-closed): payload do lock anterior ausente ou inválido; journal preservado")
		}
	}
	if !journal.HadManifest && !journal.HadLock {
		return result, errors.New("rollback: recusado (fail-closed): journal não registra o estado anterior de manifest nem de lock; journal preservado para inspeção")
	}
	// A journaled backup that disappeared cannot be rolled back without
	// fabricating content — never invent state (fail closed).
	if backupAbs != "" && !pathExists(backupAbs) {
		return result, fmt.Errorf("rollback: recusado (fail-closed): backup registrado no journal não existe mais (%s); inspeção manual necessária; journal preservado", backupAbs)
	}

	// Target directory: swap the backup back, or remove a fresh install.
	if backupAbs != "" {
		if pathExists(targetAbs) {
			if err := os.RemoveAll(targetAbs); err != nil {
				return result, fmt.Errorf("rollback: remover alvo: %w", err)
			}
		}
		if err := os.Rename(backupAbs, targetAbs); err != nil {
			return result, fmt.Errorf("rollback: restaurar backup: %w", err)
		}
		result.RestoredTarget = true
	} else if pathExists(targetAbs) {
		if err := os.RemoveAll(targetAbs); err != nil {
			return result, fmt.Errorf("rollback: remover alvo da instalação: %w", err)
		}
		result.RemovedTarget = true
	}

	// Previous state files.
	if journal.HadManifest {
		if err := restoreAtomic(manifestAbs, oldManifest); err != nil {
			return result, fmt.Errorf("rollback: restaurar manifest: %w", err)
		}
		result.RestoredManifest = true
	} else if err := os.Remove(manifestAbs); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("rollback: remover manifest: %w", err)
	}
	if journal.HadLock {
		if err := restoreAtomic(lockAbs, oldLock); err != nil {
			return result, fmt.Errorf("rollback: restaurar lock: %w", err)
		}
		result.RestoredLock = true
	} else if err := os.Remove(lockAbs); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("rollback: remover lock: %w", err)
	}

	// Staging is removed only when OroTools' own marker proves ownership.
	if stageAbs != "" && pathExists(stageAbs) {
		if ownedByUs(stageAbs) {
			if err := os.RemoveAll(stageAbs); err != nil {
				return result, fmt.Errorf("rollback: remover stage: %w", err)
			}
			result.RemovedStage = true
		} else {
			result.LeftOvers = append(result.LeftOvers, stageAbs)
		}
	}

	if err := os.Remove(inspect.Path); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("rollback: remover journal: %w", err)
	}
	return result, nil
}

// validJournalTarget accepts exactly the target shape beginTransaction ever
// records: "<agent-skills-dir>/<skill-name>" (targets.go SkillsDir contract).
func validJournalTarget(target string) bool {
	if target == "" {
		return false
	}
	_, err := AgentFromSkillsDir(path.Dir(filepath.ToSlash(target)))
	return err == nil && cleanSegment(path.Base(filepath.ToSlash(target)))
}

// validateJournalInternalPath accepts only absolute paths inside projectRoot
// whose base name carries the expected internal prefix, refusing a tampered
// journal from deleting or renaming arbitrary directories.
func validateJournalInternalPath(absRoot, raw, prefix string) (string, error) {
	if raw == "" {
		return "", errors.New("caminho vazio")
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("caminho %q deveria ser absoluto", raw)
	}
	rel, err := filepath.Rel(absRoot, filepath.Clean(raw))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("caminho %q está fora da raiz do projeto", raw)
	}
	if !strings.HasPrefix(filepath.Base(raw), prefix) {
		return "", fmt.Errorf("caminho %q não usa o prefixo interno %q", raw, prefix)
	}
	return filepath.Clean(raw), nil
}
