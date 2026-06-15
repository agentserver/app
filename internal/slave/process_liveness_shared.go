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

// resolveProcessExe returns the real executable path of a running pid.
// Overridden per-platform by init() in process_liveness_{linux,darwin}.go.
// Windows is excluded: it has its own self-contained implementation in
// process_liveness_windows.go (OpenProcess-based, not syscall.Kill).
// The default returns an error naming runtime.GOOS so a misconfigured build-tag
// matrix is diagnosable instead of silently degrading.
var resolveProcessExe = func(pid int) (string, error) {
	return "", fmt.Errorf("resolveProcessExe not wired for GOOS=%s", runtime.GOOS)
}

func osProcessExists(pid int) bool {
	inspection, err := inspectOSProcess(pid, "")
	return err == nil && inspection == processMatch
}

func inspectOSProcess(pid int, expectedExe string) (processInspection, error) {
	return inspectOSProcessWith(pid, expectedExe, resolveProcessExe)
}

// inspectOSProcessWith is the platform-agnostic core; resolve is injected so the
// decision logic is unit-testable on Linux.
func inspectOSProcessWith(pid int, expectedExe string, resolve func(int) (string, error)) (processInspection, error) {
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
	procExe, err := resolve(pid)
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
