package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const syncLockRetry = 25 * time.Millisecond

type syncLock struct {
	file *os.File
}

func acquireSyncLock(ctx context.Context, registryPath, workspaceID, peerID string) (*syncLock, error) {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + peerID))
	directory := registryPath + ".locks"
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create registry sync lock directory: %w", err)
	}
	path := filepath.Join(directory, "sync-"+hex.EncodeToString(sum[:])+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open registry sync lock: %w", err)
	}
	retry := time.NewTicker(syncLockRetry)
	defer retry.Stop()
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &syncLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire registry sync lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-retry.C:
		}
	}
}

func (lock *syncLock) release() {
	if lock == nil || lock.file == nil {
		return
	}
	file := lock.file
	lock.file = nil
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
