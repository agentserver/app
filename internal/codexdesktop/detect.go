package codexdesktop

import (
	"errors"
	"fmt"
	"strings"
)

type Detected struct {
	Installed bool
	Version   string
}

var ErrNotFound = errors.New("codex desktop not found")

const detectNotFoundSentinel = "__codex_desktop_not_found__"

func Detect() (Detected, error) {
	return detectPlatform()
}

func detectedFromPowerShellOutput(out []byte, err error) (Detected, error) {
	output := strings.TrimSpace(string(out))
	if err != nil {
		if output == detectNotFoundSentinel {
			return Detected{Installed: false}, ErrNotFound
		}
		return Detected{Installed: false}, fmt.Errorf("detect codex desktop with PowerShell failed: %w; output: %s", err, output)
	}
	if output == "" {
		return Detected{Installed: false}, errors.New("detect codex desktop with PowerShell returned empty output")
	}
	if output == detectNotFoundSentinel {
		return Detected{Installed: false}, ErrNotFound
	}
	if output == "url-scheme" {
		output = ""
	}
	return Detected{Installed: true, Version: output}, nil
}
