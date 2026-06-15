//go:build darwin

package main

import "github.com/agentserver/agentserver-pkg/internal/codexruntime"

// codexManifestForDesktop resolves the codex manifest for the desktop onboarding
// flow. macOS: architecture-matched manifest inside the .app bundle.
func codexManifestForDesktop(installDir string) (string, error) {
	return codexruntime.BundledDarwinManifestPath()
}
