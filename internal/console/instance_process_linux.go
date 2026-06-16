//go:build linux

package console

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func instanceProcessBelongsToCurrentUser(pid int) bool {
	return instanceProcessBelongsToCurrentUserWith(pid, currentUID(), func(pid int) (int, bool) {
		body, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return 0, false
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "Uid:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, false
			}
			uid, err := strconv.Atoi(fields[1])
			return uid, err == nil
		}
		return 0, false
	})
}
