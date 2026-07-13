package codexdesktop

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

type processSnapshotPayload struct {
	CurrentUserSID   string                 `json:"current_user_sid"`
	CurrentSessionID uint32                 `json:"current_session_id"`
	Processes        []processSnapshotEntry `json:"processes"`
}

type processSnapshotEntry struct {
	PID               uint32 `json:"pid"`
	PackageFamilyName string `json:"package_family_name"`
	InstallLocation   string `json:"install_location"`
	OwnerSID          string `json:"owner_sid"`
	SessionID         uint32 `json:"session_id"`
}

func parseProcessSnapshot(out []byte, expectedPackageFamily, expectedInstallLocation string) (ProcessSnapshot, error) {
	if expectedPackageFamily != ChatGPTClassicPackageFamily && expectedPackageFamily != CodexPackageFamily {
		return nil, fmt.Errorf("untrusted expected package family %q", expectedPackageFamily)
	}
	expectedLocation, err := normalizeWindowsInstallLocation(expectedInstallLocation)
	if err != nil {
		return nil, fmt.Errorf("invalid expected install location: %w", err)
	}
	var payload processSnapshotPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("parse ChatGPT/Codex process snapshot: %w", err)
	}
	if strings.TrimSpace(payload.CurrentUserSID) == "" {
		return nil, fmt.Errorf("process snapshot omitted current user SID")
	}
	result := ProcessSnapshot{}
	for _, process := range payload.Processes {
		if process.PID == 0 || process.PackageFamilyName != expectedPackageFamily {
			continue
		}
		if process.OwnerSID != payload.CurrentUserSID || process.SessionID != payload.CurrentSessionID {
			continue
		}
		location, err := normalizeWindowsInstallLocation(process.InstallLocation)
		if err != nil || !strings.EqualFold(location, expectedLocation) {
			continue
		}
		result[process.PID] = struct{}{}
	}
	return result, nil
}

func normalizeWindowsInstallLocation(location string) (string, error) {
	location = strings.TrimSpace(location)
	if location == "" || strings.ContainsRune(location, '\x00') {
		return "", fmt.Errorf("empty or malformed Windows path")
	}
	location = strings.ReplaceAll(location, `\`, "/")
	if strings.HasPrefix(location, "//?/") {
		location = strings.TrimPrefix(location, "//?/")
		if len(location) >= 4 && strings.EqualFold(location[:4], "UNC/") {
			return "", fmt.Errorf("Windows UNC final path %q is not a drive path", location)
		}
	}
	normalized := path.Clean(location)
	if len(normalized) < 3 || normalized[1] != ':' || normalized[2] != '/' ||
		(normalized[0] < 'A' || normalized[0] > 'Z') && (normalized[0] < 'a' || normalized[0] > 'z') {
		return "", fmt.Errorf("Windows path %q is not drive-absolute", location)
	}
	return normalized, nil
}
