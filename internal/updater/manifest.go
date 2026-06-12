package updater

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

const AssetsHost = "assets.agent.cs.ac.cn"

type Manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

func (m Manifest) Validate() error {
	if _, err := parseVersion(m.Version); err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}
	if m.URL != strings.TrimSpace(m.URL) {
		return fmt.Errorf("installer url must not have leading or trailing whitespace")
	}
	u, err := url.Parse(m.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid installer url")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("installer url must use https")
	}
	if !strings.EqualFold(u.Hostname(), AssetsHost) {
		return fmt.Errorf("installer url host %q is not allowed", u.Hostname())
	}
	if err := validateSHA256(m.SHA256); err != nil {
		return err
	}
	if m.Size < 0 {
		return fmt.Errorf("size must not be negative")
	}
	return nil
}

func validateSHA256(s string) error {
	if s != strings.TrimSpace(s) {
		return fmt.Errorf("sha256 must not have leading or trailing whitespace")
	}
	if len(s) != 64 {
		return fmt.Errorf("sha256 must be 64 hex characters")
	}
	if _, err := hex.DecodeString(s); err != nil {
		return fmt.Errorf("sha256 must be hex: %w", err)
	}
	return nil
}
