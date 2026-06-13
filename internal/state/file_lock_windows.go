//go:build windows

package state

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type stateFileLock struct {
	file *os.File
}

func acquireStateFileLock(path string) (*stateFileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir state lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock state file: %w", err)
	}
	return &stateFileLock{file: f}, nil
}

func (l *stateFileLock) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &overlapped)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
