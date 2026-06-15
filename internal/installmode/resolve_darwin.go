//go:build darwin

package installmode

import "github.com/agentserver/agentserver-pkg/internal/paths"

// Path resolves the install-mode.json location for the running binary.
// macOS: a signed .app's Contents/MacOS is read-only, so use the writable
// InstallRoot (~/.agentserver-app) instead.
func Path() (string, error) {
	p, err := paths.Default()
	if err != nil {
		return "", err
	}
	return PathForWritable(p.InstallRoot), nil
}
