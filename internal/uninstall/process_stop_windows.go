//go:build windows

package uninstall

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func stopInstallProcesses(ctx context.Context, appDir string, names []string) error {
	if appDir == "" || len(names) == 0 {
		return nil
	}
	script := `$ErrorActionPreference = 'Stop'
$installDir = [System.IO.Path]::GetFullPath($env:AGENTSERVER_UNINSTALL_APP_DIR).TrimEnd('\')
$names = @($env:AGENTSERVER_UNINSTALL_PROCESS_NAMES -split ';' | Where-Object { $_ })
$filter = {
  if (-not $_.ExecutablePath) { return $false }
  if ($names -notcontains $_.Name) { return $false }
  $exe = [System.IO.Path]::GetFullPath($_.ExecutablePath)
  return $exe.StartsWith($installDir + '\', [System.StringComparison]::OrdinalIgnoreCase)
}
$procs = @(Get-CimInstance Win32_Process | Where-Object $filter)
foreach ($p in $procs) {
  Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
}
`
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(),
		"AGENTSERVER_UNINSTALL_APP_DIR="+appDir,
		"AGENTSERVER_UNINSTALL_PROCESS_NAMES="+strings.Join(names, ";"),
	)
	process.HideWindow(cmd)
	return cmd.Run()
}
