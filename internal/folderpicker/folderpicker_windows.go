//go:build windows

package folderpicker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func selectFolder(ctx context.Context) (string, error) {
	script := `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = '选择允许被远程控制的文件夹'
$dialog.ShowNewFolderButton = $true
$result = $dialog.ShowDialog()
if ($result -eq [System.Windows.Forms.DialogResult]::OK -and -not [string]::IsNullOrWhiteSpace($dialog.SelectedPath)) {
  [Console]::Out.WriteLine($dialog.SelectedPath)
}
`
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile",
		"-STA",
		"-ExecutionPolicy", "Bypass",
		"-Command", script,
	)
	process.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("select folder: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
