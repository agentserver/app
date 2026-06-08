//go:build windows

package browser

import (
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func openPlatform(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	process.HideWindow(cmd)
	return cmd.Start()
}
