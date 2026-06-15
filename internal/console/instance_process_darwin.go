//go:build darwin

package console

import (
	"os/exec"
	"strconv"
)

func instanceProcessBelongsToCurrentUser(pid int) bool {
	return instanceProcessBelongsToCurrentUserWith(pid, currentUID(), func(pid int) (int, bool) {
		out, err := exec.Command("ps", "-o", "uid=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return 0, false
		}
		return parseUIDFromPS(string(out))
	})
}
