package loom

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/process"
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
	CodexHome              string
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
	if cfg.CodexHome != "" {
		if err := os.MkdirAll(cfg.CodexHome, 0o755); err != nil {
			return fmt.Errorf("mkdir codex home: %w", err)
		}
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
	lines := []string{
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
		"  bin: " + quote(filepath.ToSlash(cfg.CodexBin)),
		"  workdir: " + quote(filepath.ToSlash(cfg.CodexWorkDir)),
	}
	if cfg.CodexHome != "" {
		lines = append(lines, "  codex_home: "+quote(filepath.ToSlash(cfg.CodexHome)))
	}
	lines = append(lines,
		"  extra_args: []",
		"",
		"discovery:",
		"  display_name: "+quote(cfg.DisplayName),
		"  description: "+quote(cfg.Description),
		"  skills: []",
		"",
		"listen_addr: "+quote("127.0.0.1:0"),
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
		"  url: "+quote(observerURL),
		"  workspace_id: "+quote(cfg.WorkspaceID),
		"  workspace_name: "+quote(cfg.WorkspaceName),
		"  agent_id: "+quote(observerAgentID),
		"  api_key: "+quote(observerAPIKey),
		"  token_state_path: "+quote(filepath.ToSlash(observerTokenStatePath)),
		"",
		"driver_defaults:",
		"  target_display_name: "+quote(cfg.TargetDisplay),
		"  task_timeout_sec: 600",
		"  audit_log_dir: "+quote(filepath.ToSlash(cfg.AuditLogDir)),
		"  disable_uid_check: true",
		"  max_dir_cache_entries: 50000",
		"  artifact_transport: peer_proxy",
		"",
	)
	body := strings.Join(lines, "\n")
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

type driverBackgroundProcess struct {
	process *os.Process
	stdin   *os.File
	done    chan struct{}
}

var driverBackgroundProcesses = struct {
	sync.Mutex
	byKey map[string]driverBackgroundProcess
}{byKey: map[string]driverBackgroundProcess{}}

var driverMCPStartupWait = 3 * time.Second

func StartDriverMCPServer(exe, configPath string) error {
	if exe == "" || configPath == "" {
		return nil
	}
	if _, err := os.Stat(exe); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	key := exe + "\x00serve-mcp\x00" + configPath
	driverBackgroundProcesses.Lock()
	if existing, ok := driverBackgroundProcesses.byKey[key]; ok {
		select {
		case <-existing.done:
			delete(driverBackgroundProcesses.byKey, key)
			_ = existing.stdin.Close()
		default:
			driverBackgroundProcesses.Unlock()
			return nil
		}
	}
	driverBackgroundProcesses.Unlock()

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("open driver mcp stdin pipe: %w", err)
	}
	cmd := exec.Command(exe, "serve-mcp", "--config", configPath)
	cmd.Stdin = stdinReader
	cmd.Stdout = nil
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		return fmt.Errorf("open driver mcp stderr: %w", err)
	}
	process.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		return err
	}
	_ = stdinReader.Close()
	done := make(chan struct{})
	entry := driverBackgroundProcess{process: cmd.Process, stdin: stdinWriter, done: done}
	ready := make(chan struct{})
	var readyOnce sync.Once
	markReady := func() {
		readyOnce.Do(func() {
			close(ready)
		})
	}

	driverBackgroundProcesses.Lock()
	if existing, ok := driverBackgroundProcesses.byKey[key]; ok {
		driverBackgroundProcesses.Unlock()
		_ = stdinWriter.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		select {
		case <-existing.done:
			return StartDriverMCPServer(exe, configPath)
		default:
			return nil
		}
	}
	driverBackgroundProcesses.byKey[key] = entry
	driverBackgroundProcesses.Unlock()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "driver: tunnel connected") {
				markReady()
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		_ = stdinWriter.Close()
		driverBackgroundProcesses.Lock()
		if current, ok := driverBackgroundProcesses.byKey[key]; ok && current.done == done {
			delete(driverBackgroundProcesses.byKey, key)
		}
		driverBackgroundProcesses.Unlock()
		close(done)
	}()
	select {
	case <-ready:
	case <-done:
	case <-time.After(driverMCPStartupWait):
	}
	return nil
}

func stopDriverBackgroundProcessesForTest() {
	driverBackgroundProcesses.Lock()
	entries := make([]driverBackgroundProcess, 0, len(driverBackgroundProcesses.byKey))
	for key, entry := range driverBackgroundProcesses.byKey {
		entries = append(entries, entry)
		delete(driverBackgroundProcesses.byKey, key)
	}
	driverBackgroundProcesses.Unlock()
	for _, entry := range entries {
		_ = entry.stdin.Close()
		if entry.process != nil {
			_ = entry.process.Kill()
		}
		select {
		case <-entry.done:
		default:
		}
	}
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
	mcpErr := StartDriverMCPServer(exe, configPath)
	cmd := exec.Command(exe, "serve-daemon", "--config", configPath)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	process.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return mcpErr
}

func quote(v string) string {
	return strconv.Quote(v)
}
