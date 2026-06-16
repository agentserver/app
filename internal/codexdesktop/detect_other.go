//go:build !windows && !darwin

package codexdesktop

func detectPlatform() (Detected, error) {
	return Detected{Installed: false}, ErrNotFound
}
