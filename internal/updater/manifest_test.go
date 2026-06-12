package updater

import "testing"

func TestManifestValidateAcceptsAssetsHTTPSInstaller(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    123,
		Notes:   "release notes",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestManifestValidateAcceptsMixedCaseAssetsHost(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://ASSETS.AGENT.CS.AC.CN/agentserver-app.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestManifestValidateRejectsInvalidVersion(t *testing.T) {
	m := Manifest{
		Version: "+0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected invalid version error")
	}
}

func TestManifestValidateRejectsPaddedVersion(t *testing.T) {
	m := Manifest{
		Version: " 0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected padded version error")
	}
}

func TestManifestValidateRejectsMissingSHA256(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected missing sha256 error")
	}
}

func TestManifestValidateRejectsPaddedSHA256(t *testing.T) {
	tests := []string{
		" 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef ",
	}
	for _, sha := range tests {
		m := Manifest{
			Version: "0.1.2",
			URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
			SHA256:  sha,
		}
		if err := m.Validate(); err == nil {
			t.Fatalf("expected padded sha256 error for %q", sha)
		}
	}
}

func TestManifestValidateRejectsWrongLengthSHA256(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  "0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected wrong-length sha256 error")
	}
}

func TestManifestValidateRejectsNonHexSHA256(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected non-hex sha256 error")
	}
}

func TestManifestValidateRejectsNonHTTPSURL(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "http://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected non-HTTPS URL error")
	}
}

func TestManifestValidateRejectsPaddedURL(t *testing.T) {
	tests := []string{
		" https://assets.agent.cs.ac.cn/agentserver-app.exe",
		"https://assets.agent.cs.ac.cn/agentserver-app.exe ",
	}
	for _, installerURL := range tests {
		m := Manifest{
			Version: "0.1.2",
			URL:     installerURL,
			SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		}
		if err := m.Validate(); err == nil {
			t.Fatalf("expected padded URL error for %q", installerURL)
		}
	}
}

func TestManifestValidateRejectsAssetsHostSuffixBypass(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn.evil.com/agentserver-app.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected host allowlist error")
	}
}

func TestManifestValidateRejectsAssetsHostUserinfoBypass(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn@evil.com/agentserver-app.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected host allowlist error")
	}
}

func TestManifestValidateRejectsNegativeSize(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    -1,
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected negative size error")
	}
}

func TestManifestValidateRejectsURLOutsideAssetsHost(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://example.com/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected host allowlist error")
	}
}
