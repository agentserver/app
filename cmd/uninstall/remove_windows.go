//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func removeInstallDirLater(dir string) error {
	if dir == "" {
		return nil
	}
	quoted := strings.ReplaceAll(dir, `"`, `\"`)
	cmd := exec.Command("cmd.exe", "/C", fmt.Sprintf(`ping 127.0.0.1 -n 3 >NUL & rmdir /S /Q "%s"`, quoted))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
