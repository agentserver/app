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

func TestManifestValidateRejectsMissingSHA256(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected missing sha256 error")
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
