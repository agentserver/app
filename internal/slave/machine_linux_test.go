//go:build linux

package slave

import (
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMachineStoreDoesNotPublishPartialFinalOnWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")

	withFileSizeLimit(t, 0, func() {
		if _, err := NewMachineStore(path).Ensure("61414-PC"); err == nil {
			t.Fatal("expected write failure")
		}
	})

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("machine.json exists after write failure: %v", err)
	}

	assertNoMachineTempFiles(t, filepath.Dir(path))
}

func withFileSizeLimit(t *testing.T, limit uint64, fn func()) {
	t.Helper()

	var old unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &old); err != nil {
		t.Skipf("get file size limit: %v", err)
	}

	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)

	next := old
	next.Cur = limit
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &next); err != nil {
		t.Skipf("set file size limit: %v", err)
	}
	defer func() {
		if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &old); err != nil {
			t.Fatalf("restore file size limit: %v", err)
		}
	}()

	fn()
}
