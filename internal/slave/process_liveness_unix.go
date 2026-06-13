//go:build !windows

package slave

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func osProcessExists(pid int) bool {
	inspection, err := inspectOSProcess(pid, "")
	return err == nil && inspection == processMatch
}

func inspectOSProcess(pid int, expectedExe string) (processInspection, error) {
	if pid <= 0 {
		return processMissing, nil
	}
	if err := syscall.Kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return processMissing, nil
		}
		if !errors.Is(err, syscall.EPERM) {
			return processUnknown, err
		}
	}
	if strings.TrimSpace(expectedExe) == "" {
		return processMatch, nil
	}
	if runtime.GOOS != "linux" {
		return processMatch, nil
	}
	procExe, err := os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "exe"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processMissing, nil
		}
		return processUnknown, err
	}
	matches, err := sameExecutable(procExe, expectedExe)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processMissing, nil
		}
		return processUnknown, err
	}
	if matches {
		return processMatch, nil
	}
	return processMismatch, nil
}

func terminateUntrackedProcess(ctx context.Context, pid int, expectedExe string, timeout time.Duration) error {
	inspection, err := inspectOSProcess(pid, expectedExe)
	if err != nil {
		return err
	}
	if inspection != processMatch {
		return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
		}
		return err
	}
	return waitForProcessExit(ctx, pid, timeout)
}

func sameExecutable(got, want string) (bool, error) {
	got = strings.TrimSuffix(got, " (deleted)")
	gotAbs, err := filepath.Abs(got)
	if err != nil {
		return false, err
	}
	wantAbs, err := filepath.Abs(want)
	if err != nil {
		return false, err
	}
	if gotAbs == wantAbs {
		return true, nil
	}
	gotInfo, err := os.Stat(gotAbs)
	if err != nil {
		return false, fmt.Errorf("stat got executable: %w", err)
	}
	wantInfo, err := os.Stat(wantAbs)
	if err != nil {
		return false, fmt.Errorf("stat expected executable: %w", err)
	}
	return os.SameFile(gotInfo, wantInfo), nil
}
