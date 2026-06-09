package loom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDriverConfigDefaultsObserver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi-agent", "driver.yaml")

	err := WriteDriverConfig(path, DriverConfig{
		ServerURL:   "https://agent.cs.ac.cn",
		ServerName:  "driver-abc123",
		SandboxID:   "sb-1",
		TunnelToken: "tunnel-token",
		ProxyToken:  "sandbox-proxy-token",
		WorkspaceID: "ws-1",
		ShortID:     "abc123",
	})
	if err != nil {
		t.Fatalf("WriteDriverConfig: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	tokenPath := filepath.ToSlash(filepath.Join(filepath.Dir(path), "observer.token"))
	for _, want := range []string{
		"observer:",
		"  enabled: true",
		`  url: "https://loom.nj.cs.ac.cn:10062/"`,
		`  workspace_id: "ws-1"`,
		`  workspace_name: ""`,
		`  agent_id: "driver-abc123"`,
		`  api_key: "sandbox-proxy-token"`,
		`  token_state_path: "` + tokenPath + `"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "telemetry_enabled") {
		t.Fatalf("driver.yaml contains unsupported observer telemetry field:\n%s", text)
	}
}
