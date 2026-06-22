// Package codex writes/merges ~/.codex/config.toml for the codex CLI.
package codex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
)

type Settings struct {
	Provider                string // e.g. "modelserver"
	Model                   string // e.g. "gpt-5.5"
	ModelReasoningEffort    string // e.g. "high"
	ApprovalsReviewer       string // e.g. "guardian_subagent"
	SandboxMode             string // e.g. "danger-full-access"
	WindowsSandbox          string // e.g. "unelevated"
	DeveloperInstructions   string
	BaseURL                 string // e.g. "https://code.ai.cs.ac.cn/v1"
	EnvKey                  string // e.g. "OPENAI_API_KEY"
	ExperimentalBearerToken string // e.g. "agentserver-local-proxy"
	WireAPI                 string // e.g. "responses"
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
	LocalProxyAPIKeyEnv = "AGENTSERVER_CODEX_LOCAL_API_KEY"
	// LegacyLocalProxyAPIKeyValue is retained for the Windows desktop launcher
	// path. Linux headless model proxy access must use a per-user random token.
	LegacyLocalProxyAPIKeyValue = "agentserver-local-proxy"
)

// ModelserverProxySettings returns Settings that point Codex at the local
// model proxy. It leaves Model unset so UpdateConfig preserves whatever the
// user previously selected (via agentctl/agentserver set-model). When no
// config.toml exists yet, UpdateConfig falls back to the default model.
func ModelserverProxySettings(baseURL, bearerToken string) Settings {
	s := ModelserverSettings()
	s.Model = "" // provider-only update; do not clobber user-selected model
	s.BaseURL = baseURL
	s.EnvKey = ""
	s.ExperimentalBearerToken = strings.TrimSpace(bearerToken)
	return s
}

// SetModel rewrites only the model field of the Codex config at path, preserving
// the provider, base_url, experimental_bearer_token (incl. the per-user local-proxy
// token used on Linux headless), wire_api, and all other settings. It seeds a
// valid proxy-pointed config if the file does not yet exist.
func SetModel(path, model string) error {
	settings := existingModelserverSettings(path)
	settings.Model = model
	return UpdateConfig(path, settings)
}

// existingModelserverSettings returns the modelserver provider settings currently
// in path, preserving base_url + experimental_bearer_token; defaults to the
// legacy local-proxy token when the file or fields are absent.
func existingModelserverSettings(path string) Settings {
	settings := ModelserverProxySettings(modelproxy.DefaultBaseURL, LegacyLocalProxyAPIKeyValue)
	b, err := os.ReadFile(path)
	if err != nil {
		return settings // file missing -> defaults
	}
	var root map[string]any
	if _, err := toml.Decode(string(b), &root); err != nil {
		return settings
	}
	providers, _ := root["model_providers"].(map[string]any)
	if providers == nil {
		return settings
	}
	provider, _ := providers["modelserver"].(map[string]any)
	if provider == nil {
		return settings
	}
	if base, ok := provider["base_url"].(string); ok && base != "" {
		settings.BaseURL = base
	}
	if tok, ok := provider["experimental_bearer_token"].(string); ok && tok != "" {
		settings.ExperimentalBearerToken = tok
	}
	return settings
}

const (
	defaultModelReasoningEffort  = "high"
	defaultApprovalsReviewer     = "guardian_subagent"
	defaultSandboxMode           = "danger-full-access"
	defaultWindowsSandbox        = "unelevated"
	defaultDeveloperInstructions = "请始终使用简体中文与用户交流；除非用户明确要求其他语言。"
	maxConfigBackups             = 5
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
		if err := writeConfigBackup(path, b); err != nil {
			return fmt.Errorf("backup config.toml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	root["model_provider"] = s.Provider
	// Caller-supplied Model always wins. If the caller omits it (provider-only
	// update from the launcher), preserve whatever the user previously
	// selected; only seed a default when the field is missing entirely (first
	// write).
	if s.Model != "" {
		root["model"] = s.Model
	} else if _, ok := root["model"].(string); !ok {
		root["model"] = ModelserverSettings().Model
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
	provider, _ := providers[s.Provider].(map[string]any)
	if provider == nil {
		provider = map[string]any{}
	}
	provider["name"] = s.Provider
	provider["base_url"] = s.BaseURL
	provider["wire_api"] = s.WireAPI
	if s.EnvKey != "" {
		provider["env_key"] = s.EnvKey
	} else {
		delete(provider, "env_key")
	}
	if s.ExperimentalBearerToken != "" {
		provider["experimental_bearer_token"] = s.ExperimentalBearerToken
	} else {
		delete(provider, "experimental_bearer_token")
	}
	providers[s.Provider] = provider
	root["model_providers"] = providers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return writeConfigFile(path, buf.Bytes())
}

func HasModelserverDirectConfig(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read config.toml: %w", err)
	}
	root := map[string]any{}
	if _, err := toml.Decode(string(b), &root); err != nil {
		return false, fmt.Errorf("parse existing config.toml: %w", err)
	}
	settings := ModelserverSettings()
	if stringSetting(root, "model_provider") != settings.Provider {
		return false, nil
	}
	providers := tableSetting(root, "model_providers")
	if providers == nil {
		return false, nil
	}
	provider := tableSetting(providers, settings.Provider)
	if provider == nil {
		return false, nil
	}
	return stringSetting(provider, "name") == settings.Provider &&
		stringSetting(provider, "base_url") == settings.BaseURL &&
		stringSetting(provider, "env_key") == settings.EnvKey &&
		stringSetting(provider, "wire_api") == settings.WireAPI, nil
}

func tableSetting(root map[string]any, key string) map[string]any {
	table, _ := root[key].(map[string]any)
	return table
}

func stringSetting(root map[string]any, key string) string {
	value, _ := root[key].(string)
	return strings.TrimSpace(value)
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
		if err := writeConfigBackup(path, b); err != nil {
			return fmt.Errorf("backup config.toml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	entry, _ := servers[name].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["command"] = server.Command
	entry["args"] = append([]string(nil), server.Args...)
	if server.StartupTimeoutSec > 0 {
		entry["startup_timeout_sec"] = server.StartupTimeoutSec
	} else {
		delete(entry, "startup_timeout_sec")
	}
	if server.ToolTimeoutSec > 0 {
		entry["tool_timeout_sec"] = server.ToolTimeoutSec
	} else {
		delete(entry, "tool_timeout_sec")
	}
	if server.Enabled != nil {
		entry["enabled"] = *server.Enabled
	} else {
		delete(entry, "enabled")
	}
	if len(server.Env) > 0 {
		entry["env"] = server.Env
	} else {
		delete(entry, "env")
	}
	if server.Cwd != "" {
		entry["cwd"] = server.Cwd
	} else {
		delete(entry, "cwd")
	}
	servers[name] = entry
	root["mcp_servers"] = servers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return writeConfigFile(path, buf.Bytes())
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
	if err := writeConfigBackup(path, b); err != nil {
		return fmt.Errorf("backup config.toml: %w", err)
	}
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
	return writeConfigFile(path, buf.Bytes())
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

var writeConfigBackup = writeConfigBackupFile

func writeConfigBackupFile(path string, body []byte) error {
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
	if err := writeConfigFile(backup, body); err != nil {
		return err
	}
	return pruneConfigBackups(path)
}

func writeConfigFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	tmpPath = ""
	return os.Chmod(path, 0o600)
}

func pruneConfigBackups(path string) error {
	matches, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		return err
	}
	type backupFile struct {
		path string
		ts   int64
	}
	backups := make([]backupFile, 0, len(matches))
	prefix := filepath.Base(path) + ".bak."
	for _, match := range matches {
		suffix := strings.TrimPrefix(filepath.Base(match), prefix)
		ts, _ := strconv.ParseInt(suffix, 10, 64)
		backups = append(backups, backupFile{path: match, ts: ts})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].ts == backups[j].ts {
			return backups[i].path > backups[j].path
		}
		return backups[i].ts > backups[j].ts
	})
	if len(backups) <= maxConfigBackups {
		return nil
	}
	var errs []error
	for _, backup := range backups[maxConfigBackups:] {
		if err := os.Remove(backup.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
