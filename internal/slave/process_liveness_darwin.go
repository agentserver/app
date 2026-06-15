//go:build darwin

package slave

import (
	"fmt"
	"os/exec"
	"strings"
)

func init() {
	resolveProcessExe = func(pid int) (string, error) {
		out, err := exec.Command("ps", "-o", "comm=", "-p", fmt.Sprint(pid)).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
}
