//go:build windows

package browser

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/process"
	"golang.org/x/sys/windows"
)

func openPlatform(ctx context.Context, url string) error {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("resolve Windows system directory: %w", err)
	}
	if !filepath.IsAbs(systemDirectory) {
		return fmt.Errorf("Windows system directory is not absolute: %q", systemDirectory)
	}
	rundll32 := filepath.Join(systemDirectory, "rundll32.exe")
	if !filepath.IsAbs(rundll32) {
		return fmt.Errorf("rundll32 path is not absolute: %q", rundll32)
	}
	urlDLL := filepath.Join(systemDirectory, "url.dll")
	if !filepath.IsAbs(urlDLL) {
		return fmt.Errorf("url.dll path is not absolute: %q", urlDLL)
	}
	cmd := exec.CommandContext(ctx, rundll32, urlDLL+",FileProtocolHandler", url)
	process.HideWindow(cmd)
	return cmd.Run()
}
