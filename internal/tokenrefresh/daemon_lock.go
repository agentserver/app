package tokenrefresh

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrDaemonAlreadyRunning = errors.New("tokenrefresh: daemon already running")

type DaemonLock struct {
	file *os.File
}

func DefaultDaemonLockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".agentserver-app", "token-refresher.lock"), nil
}

func AcquireDaemonLock(path string) (*DaemonLock, error) {
	if path == "" {
		return nil, fmt.Errorf("tokenrefresh: lock path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFileNonblocking(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrDaemonAlreadyRunning) {
			return nil, err
		}
		return nil, fmt.Errorf("lock token refresh daemon: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, err
	}
	return &DaemonLock{file: f}, nil
}

func (l *DaemonLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(err, closeErr)
}
