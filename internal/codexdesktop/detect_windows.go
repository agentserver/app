//go:build windows

package codexdesktop

import (
	_ "embed"
	"fmt"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

//go:embed detect_windows.ps1
var detectWindowsPowerShell string

func detectPlatform() (Detected, error) {
	powerShell, err := systemExecutablePath("powershell.exe")
	if err != nil {
		return Detected{}, fmt.Errorf("resolve system PowerShell: %w", err)
	}
	script := "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';\n" +
		detectWindowsPowerShell + "\nGet-ChatGPTCodexDetection | ConvertTo-Json -Compress"
	cmd := exec.Command(powerShell, "-NoProfile", "-NonInteractive", "-Command", script)
	process.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return detectedFromPowerShellOutput(out, err)
}
