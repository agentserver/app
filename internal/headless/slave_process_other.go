//go:build !unix

package headless

import "os/exec"

func configureSlaveProcess(_ *exec.Cmd) {}

func terminateSlaveProcess(cmd *exec.Cmd) {
	killSlaveProcess(cmd)
}

func killSlaveProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
