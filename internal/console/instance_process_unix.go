//go:build !windows

package console

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func instanceProcessBelongsToCurrentUser(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "linux" {
		return linuxProcessBelongsToCurrentUser(pid)
	}
	return syscall.Kill(pid, 0) == nil
}

func linuxProcessBelongsToCurrentUser(pid int) bool {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return false
		}
		uid, err := strconv.Atoi(fields[1])
		return err == nil && uid == os.Getuid()
	}
	return false
}
