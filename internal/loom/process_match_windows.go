//go:build windows

package loom

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func inspectOSDriverProcess(pid int) (DriverProcessMetadata, bool, error) {
	if pid <= 0 {
		return DriverProcessMetadata{}, false, nil
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return DriverProcessMetadata{}, false, nil
		}
		return DriverProcessMetadata{}, false, err
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return DriverProcessMetadata{}, false, err
	}
	if exitCode != windowsStillActive {
		return DriverProcessMetadata{}, false, nil
	}
	exe, err := processImagePathFromHandle(handle)
	if err != nil {
		return DriverProcessMetadata{}, false, err
	}
	created, err := windowsProcessCreatedAt(handle)
	if err != nil {
		return DriverProcessMetadata{}, false, err
	}
	return DriverProcessMetadata{
		PID:       pid,
		Exe:       exe,
		Args:      nil,
		CreatedAt: created,
	}, true, nil
}

func terminateOSDriverProcess(ctx context.Context, pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		code, err := windows.WaitForSingleObject(handle, 20)
		if err != nil {
			return err
		}
		if code == windows.WAIT_OBJECT_0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return fmt.Errorf("driver process %d did not exit", pid)
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

func windowsProcessCreatedAt(handle windows.Handle) (string, error) {
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exit, &kernel, &user); err != nil {
		return "", err
	}
	value := (uint64(created.HighDateTime) << 32) | uint64(created.LowDateTime)
	return "windows:" + strconv.FormatUint(value, 10), nil
}
