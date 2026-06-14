//go:build unix

package headless

import (
	"os/exec"
	"syscall"
)

func configureSlaveProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateSlaveProcess(cmd *exec.Cmd) {
	signalSlaveProcessGroup(cmd, syscall.SIGTERM)
}

func killSlaveProcess(cmd *exec.Cmd) {
	signalSlaveProcessGroup(cmd, syscall.SIGKILL)
}

func signalSlaveProcessGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}
