//go:build !windows

package tokenrefresh

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFileNonblocking(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrDaemonAlreadyRunning
	}
	return err
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
