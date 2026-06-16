//go:build !darwin

package updater

// CleanupOldBundles is a no-op off macOS (no .app.old bundle rotation there).
func CleanupOldBundles() {}
