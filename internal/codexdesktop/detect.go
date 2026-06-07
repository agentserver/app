package codexdesktop

import "errors"

type Detected struct {
	Installed bool
	Version   string
}

var ErrNotFound = errors.New("Codex Desktop not found")

func Detect() (Detected, error) {
	return detectPlatform()
}
