//go:build darwin

package folderpicker

import (
	"context"
	"os/exec"
	"strings"
)

// selectFolder shows a native folder picker via AppleScript. Returns the chosen
// POSIX path, or ("", nil) when the user cancels.
func selectFolder(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx,
		"osascript", "-e",
		`POSIX path of (choose folder with prompt "选择允许被远程控制的文件夹")`)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == nil {
			return "", nil
		}
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", nil
	}
	return p, nil
}
