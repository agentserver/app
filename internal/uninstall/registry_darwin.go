//go:build darwin

package uninstall

// removeUninstallRegistry is a no-op on macOS: there is no Windows-style
// Add/Remove Programs registry. App cleanup is handled by bundle deletion.
func removeUninstallRegistry(appID string) error { return nil }
