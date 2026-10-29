package skill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const operationLockName = ".skills-operation.lock"

// ErrProjectLocked reports that another managed skill operation owns the project.
var ErrProjectLocked = errors.New("outra operação de skills já está ativa neste projeto")

type lockOwner struct {
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// ProjectLock serializes writers across processes using atomic directory creation.
// Stale locks are intentionally not removed automatically; operators must inspect
// the owner marker before recovery rather than risk breaking a live writer.
type ProjectLock struct {
	path  string
	token string
}

// AcquireProjectLock creates a per-project exclusive lock. It waits until ctx is
// cancelled; callers should provide a bounded context.
func AcquireProjectLock(ctx context.Context, projectRoot string) (*ProjectLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("acquire project lock: %w", err)
	}
	parent, err := safeJoin(projectRoot, LockDir)
	if err != nil {
		return nil, fmt.Errorf("lock path: %w", err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create lock parent: %w", err)
	}
	lockPath, err := safeJoin(projectRoot, filepath.ToSlash(filepath.Join(LockDir, operationLockName)))
	if err != nil {
		return nil, fmt.Errorf("lock path: %w", err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("create lock token: %w", err)
	}
	token := hex.EncodeToString(raw[:])
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("acquire project lock: %w", err)
		}
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			owner, _ := json.Marshal(lockOwner{Token: token, PID: os.Getpid()})
			marker := filepath.Join(lockPath, "owner.json")
			f, createErr := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if createErr != nil {
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("write lock owner: %w", createErr)
			}
			if _, writeErr := f.Write(append(owner, '\n')); writeErr != nil {
				_ = f.Close()
				_ = os.Remove(marker)
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("write lock owner: %w", writeErr)
			}
			if syncErr := f.Sync(); syncErr != nil {
				_ = f.Close()
				_ = os.Remove(marker)
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("sync lock owner: %w", syncErr)
			}
			if closeErr := f.Close(); closeErr != nil {
				_ = os.Remove(marker)
				_ = os.Remove(lockPath)
				return nil, fmt.Errorf("close lock owner: %w", closeErr)
			}
			return &ProjectLock{path: lockPath, token: token}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create project lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %s: %v", ErrProjectLocked, lockPath, ctx.Err())
		case <-ticker.C:
		}
	}
}

// Release removes only a lock whose owner token still matches this instance.
func (l *ProjectLock) Release() error {
	if l == nil {
		return nil
	}
	marker := filepath.Join(l.path, "owner.json")
	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project lock owner: %w", err)
	}
	var owner lockOwner
	if err := json.Unmarshal(data, &owner); err != nil || owner.Token == "" || owner.Token != l.token {
		return errors.New("project lock ownership changed; refusing to release another owner's lock")
	}
	if err := os.Remove(marker); err != nil {
		return fmt.Errorf("remove project lock owner: %w", err)
	}
	if err := os.Remove(l.path); err != nil {
		return fmt.Errorf("remove project lock: %w", err)
	}
	return nil
}
