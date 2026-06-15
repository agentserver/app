//go:build darwin

package codexruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// BundledDarwinManifestPath returns the path to the architecture-matched codex
// manifest shipped inside the .app bundle
// (Contents/Resources/codex-manifest-darwin-<arch>.json).
func BundledDarwinManifestPath() (string, error) {
	arch := runtime.GOARCH // arm64 | amd64
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// exe = .../星池指挥官.app/Contents/MacOS/<binary>
	resources := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources")
	p := filepath.Join(resources, fmt.Sprintf("codex-manifest-darwin-%s.json", arch))
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("bundled darwin codex manifest not found: %w", err)
	}
	return p, nil
}
