//go:build windows

package uninstall

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

func removeUninstallRegistry(appID string) error {
	key := `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + appID
	if err := registry.DeleteKey(registry.CURRENT_USER, key); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("remove uninstall registry: %w", err)
	}
	return nil
}
