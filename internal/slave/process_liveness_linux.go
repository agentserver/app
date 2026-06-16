//go:build linux

package slave

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	resolveProcessExe = func(pid int) (string, error) {
		return os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "exe"))
	}
}
