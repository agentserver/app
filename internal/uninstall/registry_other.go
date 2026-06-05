//go:build !windows

package uninstall

func removeUninstallRegistry(appID string) error { return nil }
