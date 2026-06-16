//go:build !darwin

package main

// codexManifestForDesktop resolves the codex manifest for the desktop onboarding
// flow. Windows: bundled codex-manifest.json next to the launcher (unchanged).
func codexManifestForDesktop(installDir string) (string, error) {
	return joinExe(installDir, "codex-manifest.json"), nil
}
