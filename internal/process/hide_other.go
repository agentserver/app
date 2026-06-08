//go:build !windows

package process

import "os/exec"

func HideWindow(cmd *exec.Cmd) {
	_ = cmd
}
