package codex

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestUpdateConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		`model_reasoning_effort = "high"`,
		`approvals_reviewer = "guardian_subagent"`,
		`sandbox_mode = "danger-full-access"`,
		`developer_instructions = "请始终使用简体中文与用户交流；除非用户明确要求其他语言。"`,
		`[model_providers.modelserver]`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if runtime.GOOS == "windows" {
		for _, want := range []string{
			`[windows]`,
			`sandbox = "unelevated"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing %q in:\n%s", want, s)
			}
		}
	}
	if strings.Contains(s, `[projects.`) {
		t.Errorf("unexpected project trust config in:\n%s", s)
	}
}

func TestModelserverProxySettingsUsesConfiguredLocalCredential(t *testing.T) {
	got := ModelserverProxySettings("http://127.0.0.1:53452/v1", "random-local-token")
	if got.Provider != "modelserver" {
		t.Fatalf("Provider = %q, want modelserver", got.Provider)
	}
	if got.BaseURL != "http://127.0.0.1:53452/v1" {
		t.Fatalf("BaseURL = %q, want local proxy URL", got.BaseURL)
	}
	if got.EnvKey != "" {
		t.Fatalf("EnvKey = %q, want empty local proxy env key", got.EnvKey)
	}
	if got.WireAPI != "responses" {
		t.Fatalf("WireAPI = %q, want responses", got.WireAPI)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if strings.Contains(s, "env_key") {
		t.Fatalf("proxy config should not require an environment variable:\n%s", s)
	}
	if !strings.Contains(s, `experimental_bearer_token = "random-local-token"`) {
		t.Fatalf("proxy config missing stable bearer token:\n%s", s)
	}
	if strings.Contains(s, "agentserver-local-proxy") {
		t.Fatalf("proxy config contains compiled default token:\n%s", s)
	}
}

func TestHasModelserverDirectConfigRequiresExactDirectProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if ok, err := HasModelserverDirectConfig(path); err != nil || ok {
		t.Fatalf("missing config: ok=%v err=%v, want false nil", ok, err)
	}
	writeCodexTestFile(t, path, strings.Join([]string{
		`model_provider = "modelserver"`,
		``,
		`[model_providers.modelserver]`,
		`name = "modelserver"`,
		`base_url = "http://127.0.0.1:53452/v1"`,
		`experimental_bearer_token = "random-local-token"`,
		`wire_api = "responses"`,
		``,
	}, "\n"))
	if ok, err := HasModelserverDirectConfig(path); err != nil || ok {
		t.Fatalf("proxy config: ok=%v err=%v, want false nil", ok, err)
	}
	if err := UpdateConfig(path, ModelserverSettings()); err != nil {
		t.Fatal(err)
	}
	if ok, err := HasModelserverDirectConfig(path); err != nil || !ok {
		t.Fatalf("direct config: ok=%v err=%v, want true nil", ok, err)
	}
}

func TestUpdateConfig_MergeKeepsOtherProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	prior := `model_provider = "old"
model = "gpt-4"
some_other_key = "stays"

[windows]
sandbox_private_desktop = false

[model_providers.old]
name = "old"
base_url = "https://old/v1"
`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	// Must keep [model_providers.old] and the unrelated key
	for _, want := range []string{
		`[model_providers.old]`,
		`some_other_key = "stays"`,
		`[model_providers.modelserver]`,
		`model_provider = "modelserver"`,
		`[windows]`,
		`sandbox_private_desktop = false`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in merged config:\n%s", want, s)
		}
	}
	// The emitted sandbox key is windows-only.
	if runtime.GOOS == "windows" {
		if !strings.Contains(s, `sandbox = "unelevated"`) {
			t.Errorf("missing %q in merged config:\n%s", `sandbox = "unelevated"`, s)
		}
	}
	// Backup created
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Errorf("expected backup")
	}
}

func TestUpdateConfigPreservesUnknownModelProviderFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`model_provider = "modelserver"`,
		``,
		`[model_providers.modelserver]`,
		`name = "old-name"`,
		`base_url = "https://old/v1"`,
		`env_key = "OLD_KEY"`,
		`wire_api = "chat"`,
		`custom_header = "keep"`,
		``,
	}, "\n"))

	if err := UpdateConfig(path, ModelserverSettings()); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		`custom_header = "keep"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestUpdateConfigReturnsBackupError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, `model_provider = "old"`+"\n")
	wantErr := errors.New("backup failed")
	prev := writeConfigBackup
	writeConfigBackup = func(string, []byte) error {
		return wantErr
	}
	t.Cleanup(func() {
		writeConfigBackup = prev
	})

	err := UpdateConfig(path, ModelserverSettings())
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpdateConfig error=%v, want %v", err, wantErr)
	}
}

func TestUpdateConfigWritesPrivateFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, ModelserverSettings()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o, want 0600", got)
	}
}

func TestUpdateConfigPrunesOldBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, `model_provider = "old"`+"\n")
	for i := 1; i <= maxConfigBackups+3; i++ {
		writeCodexTestFile(t, filepath.Join(dir, "config.toml.bak."+strconv.Itoa(i)), "backup\n")
	}

	if err := UpdateConfig(path, Settings{
		Provider: "modelserver",
		Model:    "gpt-5.5",
		BaseURL:  "https://code.ai.cs.ac.cn/v1",
		EnvKey:   "OPENAI_API_KEY",
		WireAPI:  "responses",
	}); err != nil {
		t.Fatal(err)
	}

	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != maxConfigBackups {
		t.Fatalf("backup count=%d want %d: %v", len(matches), maxConfigBackups, matches)
	}
	for _, old := range []string{"1", "2", "3", "4"} {
		if _, err := os.Stat(path + ".bak." + old); !os.IsNotExist(err) {
			t.Fatalf("old backup %s was not pruned, stat err=%v", old, err)
		}
	}
}

func TestUpdateMCPServerAddsDriverAndKeepsModelConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, Settings{
		Provider: "modelserver",
		Model:    "gpt-5.5",
		BaseURL:  "https://code.ai.cs.ac.cn/v1",
		EnvKey:   "OPENAI_API_KEY",
		WireAPI:  "responses",
	}); err != nil {
		t.Fatal(err)
	}

	if err := UpdateMCPServer(path, "driver", MCPServer{
		Command:           `C:\Users\61414\AppData\Local\Programs\agentserver-app\driver-agent.exe`,
		Args:              []string{"serve-mcp", "--config", `C:\Users\61414\.config\multi-agent\driver.yaml`},
		StartupTimeoutSec: 30,
		ToolTimeoutSec:    120,
		Enabled:           boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`[model_providers.modelserver]`,
		`[mcp_servers.driver]`,
		`command = "C:\\Users\\61414\\AppData\\Local\\Programs\\agentserver-app\\driver-agent.exe"`,
		`args = ["serve-mcp", "--config", "C:\\Users\\61414\\.config\\multi-agent\\driver.yaml"]`,
		`startup_timeout_sec = 30`,
		`tool_timeout_sec = 120`,
		`enabled = true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestUpdateMCPServerPreservesUnknownFieldsInNamedServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`[mcp_servers.driver]`,
		`command = "old"`,
		`args = ["old"]`,
		`custom_tools = ["keep"]`,
		``,
	}, "\n"))

	if err := UpdateMCPServer(path, "driver", MCPServer{
		Command: "agentserver",
		Args:    []string{"serve-driver-mcp"},
	}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`command = "agentserver"`,
		`args = ["serve-driver-mcp"]`,
		`custom_tools = ["keep"]`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestRemoveMCPServerRemovesOnlyNamedServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		``,
		`[mcp_servers.driver]`,
		`command = "C:\\Agentserver\\driver-agent.exe"`,
		`args = ["serve-mcp", "--config", "C:\\Users\\me\\.config\\multi-agent\\driver.yaml"]`,
		`startup_timeout_sec = 30`,
		``,
		`[mcp_servers.other]`,
		`command = "other.exe"`,
		`args = ["serve"]`,
		``,
		`[model_providers.modelserver]`,
		`name = "modelserver"`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		``,
	}, "\n"))

	if err := RemoveMCPServer(path, "driver"); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, unwanted := range []string{
		`[mcp_servers.driver]`,
		`C:\\Agentserver\\driver-agent.exe`,
		`multi-agent\\driver.yaml`,
		`startup_timeout_sec = 30`,
	} {
		if strings.Contains(s, unwanted) {
			t.Fatalf("config.toml still contains driver MCP content %q:\n%s", unwanted, s)
		}
	}
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		`[mcp_servers.other]`,
		`command = "other.exe"`,
		`[model_providers.modelserver]`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("config.toml missing preserved content %q:\n%s", want, s)
		}
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Fatalf("expected backup")
	}
}

func writeCodexTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestUpdateConfigNoWindowsSectionOnNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := UpdateConfig(path, Settings{
		Provider: "agentserver",
		BaseURL:  "http://127.0.0.1:53452/v1",
		WireAPI:  "responses",
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(b), "[windows]") {
		t.Errorf("non-windows config must not emit [windows] section:\n%s", b)
	}
}
