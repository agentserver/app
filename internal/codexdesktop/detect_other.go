//go:build !windows

package codexdesktop

func detectPlatform() (Detected, error) {
	return Detected{Installed: false}, ErrNotFound
}
