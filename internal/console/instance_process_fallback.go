//go:build !windows && !linux && !darwin

package console

import "syscall"

func instanceProcessBelongsToCurrentUser(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
