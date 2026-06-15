//go:build darwin

package uninstall

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// stopInstallProcesses enumerates processes whose executable lives under appDir
// (the .app/Contents/MacOS tree) or whose basename is in names, SIGKILLs them,
// and polls until they exit. Mirrors Windows Stop-Process + Wait-Process.
func stopInstallProcesses(ctx context.Context, appDir string, names []string) error {
	pids, err := pidsUnderAppDir(ctx, appDir, names)
	if err != nil {
		return err
	}
	for _, pid := range pids {
		_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		remaining, err := pidsUnderAppDir(ctx, appDir, names)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("processes under %s did not exit within timeout", appDir)
}

func pidsUnderAppDir(ctx context.Context, appDir string, names []string) ([]int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		comm := strings.Join(fields[1:], " ")
		matched := strings.HasPrefix(comm, appDir)
		if !matched {
			for _, n := range names {
				if filepath.Base(comm) == n {
					matched = true
					break
				}
			}
		}
		if matched {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
