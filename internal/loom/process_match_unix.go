//go:build !windows

package loom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func inspectOSDriverProcess(pid int) (DriverProcessMetadata, bool, error) {
	if pid <= 0 {
		return DriverProcessMetadata{}, false, nil
	}
	if err := syscall.Kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return DriverProcessMetadata{}, false, nil
		}
		if !errors.Is(err, syscall.EPERM) {
			return DriverProcessMetadata{}, false, err
		}
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DriverProcessMetadata{}, false, nil
		}
		return DriverProcessMetadata{}, false, err
	}
	exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return DriverProcessMetadata{}, false, nil
	}
	args, err := readProcCmdline(pid)
	if err != nil {
		return DriverProcessMetadata{}, false, nil
	}
	created, err := linuxProcessCreatedAt(pid)
	if err != nil {
		return DriverProcessMetadata{}, false, nil
	}
	return DriverProcessMetadata{
		PID:       pid,
		Exe:       exe,
		Args:      args,
		CreatedAt: created,
	}, true, nil
}

func terminateOSDriverProcess(ctx context.Context, pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return fmt.Errorf("driver process %d did not exit", pid)
}

func readProcCmdline(pid int) ([]string, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	if len(parts) == 0 {
		return nil, nil
	}
	if len(parts) > 1 {
		return parts[1:], nil
	}
	return nil, nil
}

func linuxProcessCreatedAt(pid int) (string, error) {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	text := string(stat)
	endComm := strings.LastIndex(text, ")")
	if endComm < 0 || endComm+2 >= len(text) {
		return "", fmt.Errorf("invalid proc stat")
	}
	fields := strings.Fields(text[endComm+2:])
	if len(fields) < 20 {
		return "", fmt.Errorf("invalid proc stat fields")
	}
	startTime := fields[19]
	return "linux:" + strings.TrimSpace(string(bootID)) + ":" + startTime, nil
}
