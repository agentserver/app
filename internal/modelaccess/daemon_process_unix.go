//go:build !windows

package modelaccess

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func configureDaemonProcess(cmd *exec.Cmd, logPath string) (func(), error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	logFile := devNull
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
			_ = devNull.Close()
			return nil, err
		}
		logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			_ = devNull.Close()
			return nil, err
		}
	}
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return func() {
		_ = devNull.Close()
		if logFile != devNull {
			_ = logFile.Close()
		}
	}, nil
}
