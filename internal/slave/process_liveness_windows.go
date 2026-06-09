//go:build windows

package slave

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func osProcessExists(pid int) bool {
	inspection, err := inspectOSProcess(pid, "")
	return err == nil && inspection == processMatch
}

func inspectOSProcess(pid int, expectedExe string) (processInspection, error) {
	if pid <= 0 {
		return processMissing, nil
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return processMissing, nil
		}
		return processUnknown, err
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return processUnknown, err
	}
	if exitCode != windowsStillActive {
		return processMissing, nil
	}
	if strings.TrimSpace(expectedExe) == "" {
		return processMatch, nil
	}
	got, err := processImagePathFromHandle(handle)
	if err != nil {
		return processUnknown, err
	}
	if sameWindowsPath(got, expectedExe) {
		return processMatch, nil
	}
	return processMismatch, nil
}

func terminateUntrackedProcess(ctx context.Context, pid int, expectedExe string, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
		false,
		uint32(pid),
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
		}
		return err
	}
	defer windows.CloseHandle(handle)

	inspection, err := inspectProcessHandle(handle, expectedExe)
	if err != nil {
		return err
	}
	if inspection != processMatch {
		return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
	}
	if err := windows.TerminateProcess(handle, 1); err != nil {
		var exitCode uint32
		if getErr := windows.GetExitCodeProcess(handle, &exitCode); getErr == nil && exitCode != windowsStillActive {
			return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
		}
		return err
	}
	return waitForProcessExit(ctx, pid, timeout)
}

func inspectProcessHandle(handle windows.Handle, expectedExe string) (processInspection, error) {
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return processUnknown, err
	}
	if exitCode != windowsStillActive {
		return processMissing, nil
	}
	if strings.TrimSpace(expectedExe) == "" {
		return processMatch, nil
	}
	got, err := processImagePathFromHandle(handle)
	if err != nil {
		return processUnknown, err
	}
	if sameWindowsPath(got, expectedExe) {
		return processMatch, nil
	}
	return processMismatch, nil
}

func processImagePathFromHandle(handle windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		size := uint32(len(buf))
		err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size]), nil
		}
		if len(buf) >= 32768 {
			return "", err
		}
		buf = make([]uint16, len(buf)*2)
	}
}

func sameWindowsPath(got, want string) bool {
	gotAbs, err := filepath.Abs(got)
	if err != nil {
		return false
	}
	wantAbs, err := filepath.Abs(want)
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(gotAbs), filepath.Clean(wantAbs))
}
