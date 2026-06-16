//go:build !windows && !darwin

package uninstall

func removeUninstallRegistry(appID string) error { return nil }
