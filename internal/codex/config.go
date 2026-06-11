// Package codex writes/merges ~/.codex/config.toml for the codex CLI.
package codex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Settings struct {
	Provider              string // e.g. "modelserver"
	Model                 string // e.g. "gpt-5.5"
	ModelReasoningEffort  string // e.g. "high"
	ApprovalsReviewer     string // e.g. "guardian_subagent"
	SandboxMode           string // e.g. "danger-full-access"
	WindowsSandbox        string // e.g. "unelevated"
	DeveloperInstructions string
	BaseURL               string // e.g. "https://code.ai.cs.ac.cn/v1"
	EnvKey                string // e.g. "OPENAI_API_KEY"
	WireAPI               string // e.g. "responses"
}

type MCPServer struct {
	Command           string
	Args              []string
	Env               map[string]string
	Cwd               string
	StartupTimeoutSec int
	ToolTimeoutSec    int
	Enabled           *bool
}

func ModelserverSettings() Settings {
	return Settings{
		Provider: "modelserver",
		Model:    "gpt-5.5",
		BaseURL:  "https://code.ai.cs.ac.cn/v1",
		EnvKey:   "OPENAI_API_KEY",
		WireAPI:  "responses",
	}
}

const (
	LocalProxyAPIKeyEnv   = "AGENTSERVER_CODEX_LOCAL_API_KEY"
	LocalProxyAPIKeyValue = "agentserver-local-proxy"
)

func ModelserverProxySettings(baseURL string) Settings {
	s := ModelserverSettings()
	s.BaseURL = baseURL
	s.EnvKey = LocalProxyAPIKeyEnv
	return s
}

const (
	defaultModelReasoningEffort  = "high"
	defaultApprovalsReviewer     = "guardian_subagent"
	defaultSandboxMode           = "danger-full-access"
	defaultWindowsSandbox        = "unelevated"
	defaultDeveloperInstructions = "请始终使用简体中文与用户交流；除非用户明确要求其他语言。"
)

// UpdateConfig merges Settings into the config.toml at `path`, preserving
// any unrelated top-level keys and any [model_providers.X] tables other
// than ours. The original is backed up to path.bak.<unix-ts> first.
func UpdateConfig(path string, s Settings) error {
	if s.Provider == "" {
		return errors.New("Settings.Provider required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir codex dir: %w", err)
	}

	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(b), &root); err != nil {
			return fmt.Errorf("parse existing config.toml: %w", err)
		}
		// backup
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		_ = os.WriteFile(backup, b, 0o644)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	root["model_provider"] = s.Provider
	if s.Model != "" {
		root["model"] = s.Model
	}
	root["model_reasoning_effort"] = defaultString(s.ModelReasoningEffort, defaultModelReasoningEffort)
	root["approvals_reviewer"] = defaultString(s.ApprovalsReviewer, defaultApprovalsReviewer)
	root["sandbox_mode"] = defaultString(s.SandboxMode, defaultSandboxMode)
	root["developer_instructions"] = defaultString(s.DeveloperInstructions, defaultDeveloperInstructions)
	windows, _ := root["windows"].(map[string]any)
	if windows == nil {
		windows = map[string]any{}
	}
	windows["sandbox"] = defaultString(s.WindowsSandbox, defaultWindowsSandbox)
	root["windows"] = windows
	providers, _ := root["model_providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[s.Provider] = map[string]any{
		"name":     s.Provider,
		"base_url": s.BaseURL,
		"env_key":  s.EnvKey,
		"wire_api": s.WireAPI,
	}
	root["model_providers"] = providers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func UpdateMCPServer(path, name string, server MCPServer) error {
	if name == "" {
		return errors.New("MCP server name required")
	}
	if server.Command == "" {
		return errors.New("MCP server command required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir codex dir: %w", err)
	}

	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(b), &root); err != nil {
			return fmt.Errorf("parse existing config.toml: %w", err)
		}
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		_ = os.WriteFile(backup, b, 0o644)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	entry := map[string]any{
		"command": server.Command,
		"args":    append([]string(nil), server.Args...),
	}
	if server.StartupTimeoutSec > 0 {
		entry["startup_timeout_sec"] = server.StartupTimeoutSec
	}
	if server.ToolTimeoutSec > 0 {
		entry["tool_timeout_sec"] = server.ToolTimeoutSec
	}
	if server.Enabled != nil {
		entry["enabled"] = *server.Enabled
	}
	if len(server.Env) > 0 {
		entry["env"] = server.Env
	}
	if server.Cwd != "" {
		entry["cwd"] = server.Cwd
	}
	servers[name] = entry
	root["mcp_servers"] = servers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func RemoveMCPServer(path, name string) error {
	if name == "" {
		return errors.New("MCP server name required")
	}
	root := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config.toml: %w", err)
	}
	if _, err := toml.Decode(string(b), &root); err != nil {
		return fmt.Errorf("parse existing config.toml: %w", err)
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		return nil
	}
	if _, ok := servers[name]; !ok {
		return nil
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	_ = os.WriteFile(backup, b, 0o644)
	delete(servers, name)
	if len(servers) == 0 {
		delete(root, "mcp_servers")
	} else {
		root["mcp_servers"] = servers
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
