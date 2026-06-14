//go:build windows

package modelaccess

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func configureDaemonProcess(cmd *exec.Cmd, logPath string) (func(), error) {
	cmd.Stdin = nil
	var logFile *os.File
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
			return nil, err
		}
		var err error
		logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		cmd.Stdout = nil
		cmd.Stderr = nil
	}
	process.HideWindow(cmd)
	return func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}, nil
}
