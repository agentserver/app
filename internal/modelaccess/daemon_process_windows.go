//go:build windows

package modelaccess

import (
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func configureDaemonProcess(cmd *exec.Cmd) (func(), error) {
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	process.HideWindow(cmd)
	return func() {}, nil
}
