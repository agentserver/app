package opencodedesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Detected struct {
	Installed bool
	Path      string
	Version   string
}

var (
	ErrNotFound            = errors.New("opencode desktop not found")
	ErrUnsupportedPlatform = errors.New("opencode desktop install is only supported on Windows")
)

func Detect() (Detected, error) {
	return detectPlatform()
}

func parseDetectOutput(out []byte) (Detected, error) {
	output := strings.TrimSpace(string(out))
	if output == "" {
		return Detected{}, errors.New("detect opencode desktop returned empty output")
	}
	var det Detected
	if err := json.Unmarshal([]byte(output), &det); err != nil {
		return Detected{}, fmt.Errorf("parse opencode desktop detection output: %w; output: %s", err, output)
	}
	if !det.Installed {
		return Detected{Installed: false}, ErrNotFound
	}
	return det, nil
}
