package repo

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrProjectLocked = errors.New("project is busy")

type ProjectLock struct {
	file *os.File
}

func AcquireProjectLock(projectPath string) (*ProjectLock, error) {
	canonical, err := canonicalProjectPath(projectPath)
	if err != nil {
		return nil, err
	}
	base := filepath.Join("/tmp", fmt.Sprintf("ws-%d", os.Getuid()))
	if err = ensureLockDirectory(base); err != nil {
		return nil, err
	}
	directory := filepath.Join(base, "repo-locks")
	if err = ensureLockDirectory(directory); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(filepath.Clean(canonical)))
	path := filepath.Join(directory, fmt.Sprintf("%x.lock", digest[:16]))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open project lock: %w", err)
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrProjectLocked
		}
		return nil, fmt.Errorf("acquire project lock: %w", err)
	}
	return &ProjectLock{file: file}, nil
}

func canonicalProjectPath(projectPath string) (string, error) {
	absolute, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	for {
		if _, err = os.Lstat(ancestor); err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("project path has no existing ancestor")
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	remainder, err := filepath.Rel(ancestor, absolute)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, remainder), nil
}

func ensureLockDirectory(directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create project lock directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect project lock directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("project lock directory is not a directory")
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure project lock directory: %w", err)
	}
	return nil
}

func (lock *ProjectLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return errors.Join(unlockErr, file.Close())
}
