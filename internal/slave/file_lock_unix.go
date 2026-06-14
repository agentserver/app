//go:build !windows

package slave

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type registryFileLock struct {
	file *os.File
}

func acquireRegistryFileLock(path string) (*registryFileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir slave registry lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open slave registry lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock slave registry: %w", err)
	}
	return &registryFileLock{file: f}, nil
}

func (l *registryFileLock) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
