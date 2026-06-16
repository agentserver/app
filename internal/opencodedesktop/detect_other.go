//go:build !windows

package opencodedesktop

func detectPlatform() (Detected, error) {
	return Detected{Installed: false}, ErrUnsupportedPlatform
}
