package codexdesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Status string

const (
	StatusReady               Status = "ready"
	StatusNotInstalled        Status = "not_installed"
	StatusSchemeMissing       Status = "scheme_missing"
	StatusSchemeTargetInvalid Status = "scheme_target_invalid"
	StatusLaunchFailed        Status = "launch_failed"
)

const (
	ChatGPTStoreProductID    = "9NT1R1C2HH7J"
	ChatGPTPackageFamily     = "OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0"
	LegacyCodexPackageFamily = "OpenAI.Codex_2p2nqsd0c76g0"
	ShortDisplayName         = "ChatGPT / Codex"
	LongDisplayName          = "ChatGPT 桌面应用（含 Codex）"
)

type Detected struct {
	Installed         bool   `json:"installed"`
	Version           string `json:"version"`
	Status            Status `json:"status"`
	PackageFamilyName string `json:"package_family_name"`
	InstallLocation   string `json:"install_location"`
	AppUserModelID    string `json:"app_user_model_id"`
	SchemeRegistered  bool   `json:"scheme_registered"`
	SchemeTargetValid bool   `json:"scheme_target_valid"`
}

var (
	ErrNotFound            = errors.New("ChatGPT/Codex desktop app not found")
	ErrSchemeMissing       = errors.New("codex URL scheme is missing")
	ErrSchemeTargetInvalid = errors.New("codex URL scheme target is invalid")
	ErrLaunchFailed        = errors.New("ChatGPT/Codex desktop app launch failed")
)

func Detect() (Detected, error) {
	return detectPlatform()
}

func detectedFromPowerShellOutput(out []byte, err error) (Detected, error) {
	output := strings.TrimSpace(string(out))
	if err != nil {
		return Detected{}, fmt.Errorf("detect ChatGPT/Codex desktop app with PowerShell failed: %w; output: %s", err, output)
	}
	if output == "" {
		return Detected{}, errors.New("detect ChatGPT/Codex desktop app with PowerShell returned empty output")
	}
	var det Detected
	if err := json.Unmarshal([]byte(output), &det); err != nil {
		return Detected{}, fmt.Errorf("parse ChatGPT/Codex desktop app detection output: %w", err)
	}
	if err := validateDetected(det); err != nil {
		return Detected{}, fmt.Errorf("validate ChatGPT/Codex desktop app detection output: %w", err)
	}
	switch det.Status {
	case StatusReady:
		return det, nil
	case StatusNotInstalled:
		return det, ErrNotFound
	case StatusSchemeMissing:
		return det, ErrSchemeMissing
	case StatusSchemeTargetInvalid:
		return det, ErrSchemeTargetInvalid
	default:
		return Detected{}, fmt.Errorf("unsupported ChatGPT/Codex desktop app status %q", det.Status)
	}
}

func validateDetected(det Detected) error {
	trustedPackage := det.PackageFamilyName == ChatGPTPackageFamily || det.PackageFamilyName == LegacyCodexPackageFamily
	hasPackageMetadata := strings.TrimSpace(det.PackageFamilyName) != "" || strings.TrimSpace(det.InstallLocation) != "" || det.AppUserModelID != ""

	switch det.Status {
	case StatusReady:
		if !det.Installed || !trustedPackage || strings.TrimSpace(det.InstallLocation) == "" || !det.SchemeRegistered || !det.SchemeTargetValid {
			return fmt.Errorf("inconsistent ready status")
		}
		packageFamily, _, err := parseAppUserModelID(det.AppUserModelID)
		if err != nil {
			return fmt.Errorf("invalid ready AppUserModelID: %w", err)
		}
		if packageFamily != det.PackageFamilyName {
			return fmt.Errorf("ready AppUserModelID belongs to package family %q, not %q", packageFamily, det.PackageFamilyName)
		}
	case StatusNotInstalled:
		if det.Installed || hasPackageMetadata || det.SchemeTargetValid {
			return fmt.Errorf("inconsistent not-installed status")
		}
	case StatusSchemeMissing:
		if !det.Installed || !trustedPackage || strings.TrimSpace(det.InstallLocation) == "" || det.SchemeRegistered || det.SchemeTargetValid {
			return fmt.Errorf("inconsistent scheme-missing status")
		}
	case StatusSchemeTargetInvalid:
		if !det.Installed || !trustedPackage || strings.TrimSpace(det.InstallLocation) == "" || !det.SchemeRegistered || det.SchemeTargetValid {
			return fmt.Errorf("inconsistent invalid-scheme-target status")
		}
	case StatusLaunchFailed:
		return fmt.Errorf("launch status is not valid detection output")
	default:
		return fmt.Errorf("unknown status %q", det.Status)
	}
	if det.Status != StatusReady && det.AppUserModelID != "" {
		return fmt.Errorf("non-ready status %q included an AppUserModelID", det.Status)
	}
	return nil
}

func parseAppUserModelID(aumid string) (packageFamily, applicationID string, err error) {
	if aumid == "" || aumid != strings.TrimSpace(aumid) || strings.Count(aumid, "!") != 1 {
		return "", "", fmt.Errorf("malformed AppUserModelID %q", aumid)
	}
	packageFamily, applicationID, _ = strings.Cut(aumid, "!")
	if packageFamily == "" || applicationID == "" || applicationID != strings.TrimSpace(applicationID) {
		return "", "", fmt.Errorf("malformed AppUserModelID %q", aumid)
	}
	return packageFamily, applicationID, nil
}
