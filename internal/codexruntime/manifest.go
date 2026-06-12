package codexruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Manifest struct {
	Package       string        `json:"package"`
	Platform      string        `json:"platform"`
	PinnedVersion string        `json:"pinned_version"`
	StripPrefix   string        `json:"strip_prefix"`
	CodexExe      string        `json:"codex_exe"`
	RequiredFiles []string      `json:"required_files"`
	Pinned        PinnedPackage `json:"pinned"`
}

type PinnedPackage struct {
	Integrity string   `json:"integrity"`
	Shasum    string   `json:"shasum"`
	URLs      []string `json:"urls"`
}

func LoadManifest(path string) (Manifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (m Manifest) Validate() error {
	var missing []string
	if strings.TrimSpace(m.Package) == "" {
		missing = append(missing, "package")
	}
	if strings.TrimSpace(m.Platform) == "" {
		missing = append(missing, "platform")
	}
	if strings.TrimSpace(m.PinnedVersion) == "" {
		missing = append(missing, "pinned_version")
	}
	if strings.TrimSpace(m.StripPrefix) == "" {
		missing = append(missing, "strip_prefix")
	}
	if strings.TrimSpace(m.CodexExe) == "" {
		missing = append(missing, "codex_exe")
	}
	if len(m.RequiredFiles) == 0 {
		missing = append(missing, "required_files")
	}
	if len(m.Pinned.URLs) == 0 {
		missing = append(missing, "pinned.urls")
	}
	if strings.TrimSpace(m.Pinned.Integrity) == "" {
		missing = append(missing, "pinned.integrity")
	}
	if len(missing) > 0 {
		return fmt.Errorf("codex manifest missing %s", strings.Join(missing, ", "))
	}
	return nil
}
