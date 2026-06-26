package loom

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultObserverURL = "https://loom.nj.cs.ac.cn:10062/"

type DriverConfig struct {
	ServerURL              string
	ServerName             string
	SandboxID              string
	TunnelToken            string
	ProxyToken             string
	WorkspaceID            string
	WorkspaceName          string
	ShortID                string
	DisplayName            string
	Description            string
	CodexBin               string
	CodexWorkDir           string
	AuditLogDir            string
	TargetDisplay          string
	ObserverURL            string
	ObserverAgentID        string
	ObserverAPIKey         string
	ObserverTokenStatePath string
}

func WriteDriverConfig(path string, cfg DriverConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("mkdir loom config dir: %w", err)
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "星池指挥官"
	}
	if cfg.Description == "" {
		cfg.Description = "Loom driver for Codex."
	}
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	observerURL := cfg.ObserverURL
	if observerURL == "" {
		observerURL = DefaultObserverURL
	}
	observerAgentID := cfg.ObserverAgentID
	if observerAgentID == "" {
		observerAgentID = cfg.ServerName
	}
	observerAPIKey := cfg.ObserverAPIKey
	if observerAPIKey == "" {
		observerAPIKey = cfg.ProxyToken
	}
	observerTokenStatePath := cfg.ObserverTokenStatePath
	if observerTokenStatePath == "" {
		observerTokenStatePath = filepath.Join(configDir, "observer.token")
	}
	if !filepath.IsAbs(observerTokenStatePath) {
		if abs, err := filepath.Abs(observerTokenStatePath); err == nil {
			observerTokenStatePath = abs
		}
	}
	body := strings.Join([]string{
		"server:",
		"  url: " + quote(cfg.ServerURL),
		"  name: " + quote(cfg.ServerName),
		"",
		"credentials:",
		"  sandbox_id: " + quote(cfg.SandboxID),
		"  tunnel_token: " + quote(cfg.TunnelToken),
		"  proxy_token: " + quote(cfg.ProxyToken),
		"  workspace_id: " + quote(cfg.WorkspaceID),
		"  short_id: " + quote(cfg.ShortID),
		"",
		"agent:",
		"  kind: " + quote("codex"),
		"codex:",
		"  bin: " + quote(filepath.ToSlash(cfg.CodexBin)),
		"  workdir: " + quote(filepath.ToSlash(cfg.CodexWorkDir)),
		"  extra_args: []",
		"",
		"discovery:",
		"  display_name: " + quote(cfg.DisplayName),
		"  description: " + quote(cfg.Description),
		"  skills: []",
		"",
		"listen_addr: " + quote("127.0.0.1:0"),
		"",
		"planner:",
		"  timeout_sec: 300",
		"",
		"fanout:",
		"  max_concurrency: 4",
		"  subtask_defaults:",
		"    timeout_sec: 900",
		"",
		"observer:",
		"  enabled: true",
		"  url: " + quote(observerURL),
		"  workspace_id: " + quote(cfg.WorkspaceID),
		"  workspace_name: " + quote(cfg.WorkspaceName),
		"  agent_id: " + quote(observerAgentID),
		"  api_key: " + quote(observerAPIKey),
		"  token_state_path: " + quote(filepath.ToSlash(observerTokenStatePath)),
		"",
		"driver_defaults:",
		"  target_display_name: " + quote(cfg.TargetDisplay),
		"  task_timeout_sec: 600",
		"  audit_log_dir: " + quote(filepath.ToSlash(cfg.AuditLogDir)),
		"  disable_uid_check: true",
		"  max_dir_cache_entries: 50000",
		"  artifact_transport: peer_proxy",
		"",
	}, "\n")
	return os.WriteFile(path, []byte(body), 0o600)
}

func (c DriverConfig) validate() error {
	missing := []string{}
	for name, value := range map[string]string{
		"server.url":               c.ServerURL,
		"server.name":              c.ServerName,
		"credentials.sandbox_id":   c.SandboxID,
		"credentials.tunnel_token": c.TunnelToken,
		"credentials.proxy_token":  c.ProxyToken,
		"credentials.workspace_id": c.WorkspaceID,
		"credentials.short_id":     c.ShortID,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("agentserver registration missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func StartDriverDaemon(exe, configPath string) error {
	if exe == "" || configPath == "" {
		return nil
	}
	if _, err := os.Stat(exe); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	cmd := exec.Command(exe, "serve-daemon", "--config", configPath)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

func quote(v string) string {
	return strconv.Quote(v)
}
