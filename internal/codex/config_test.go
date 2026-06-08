package codex

import (
	"os"
	"path/filepath"
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
		`[windows]`,
		`sandbox = "unelevated"`,
		`[model_providers.modelserver]`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `[projects.`) {
		t.Errorf("unexpected project trust config in:\n%s", s)
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
		`sandbox = "unelevated"`,
		`sandbox_private_desktop = false`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in merged config:\n%s", want, s)
		}
	}
	// Backup created
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Errorf("expected backup")
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
		Command: `C:\Users\61414\AppData\Local\Programs\agentserver-vscode\driver-agent.exe`,
		Args:    []string{"serve-mcp", "--config", `C:\Users\61414\.config\multi-agent\driver.yaml`},
	}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`[model_providers.modelserver]`,
		`[mcp_servers.driver]`,
		`command = "C:\\Users\\61414\\AppData\\Local\\Programs\\agentserver-vscode\\driver-agent.exe"`,
		`args = ["serve-mcp", "--config", "C:\\Users\\61414\\.config\\multi-agent\\driver.yaml"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
