//go:build !windows

package modelaccess

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDaemonProcess(cmd *exec.Cmd) (func(), error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return func() { _ = devNull.Close() }, nil
}
