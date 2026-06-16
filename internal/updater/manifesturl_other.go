//go:build !darwin

package updater

// DefaultManifestURLForPlatform returns the update manifest URL for the current
// platform. Windows/Linux keep the historical windows/ URL (Linux headless has
// its own flow; this is the desktop fallback).
func DefaultManifestURLForPlatform() string { return DefaultManifestURL }
