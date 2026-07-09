package loom

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codexdebug"
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
	CodexExtraArgs         []string
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
		"  extra_args: "+quoteListSlash(cfg.CodexExtraArgs),
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

func CodexDebugWrapperInvocation(wrapperPath, codexBin string) (string, []string) {
	wrapperPath = strings.TrimSpace(wrapperPath)
	codexBin = strings.TrimSpace(codexBin)
	if codexBin == "" {
		codexBin = "codex"
	}
	if wrapperPath == "" || samePath(wrapperPath, codexBin) {
		return codexBin, nil
	}
	if _, err := os.Stat(wrapperPath); err != nil {
		return codexBin, nil
	}
	return wrapperPath, nil
}

func WriteCodexDebugWrapperConfig(wrapperPath, codexBin string) error {
	wrapperPath = strings.TrimSpace(wrapperPath)
	codexBin = strings.TrimSpace(codexBin)
	if wrapperPath == "" || codexBin == "" || samePath(wrapperPath, codexBin) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
		return fmt.Errorf("mkdir codex debug wrapper dir: %w", err)
	}
	body := "{\n  \"codex_bin\": " + quote(codexBin) + "\n}\n"
	if err := os.WriteFile(codexdebug.ConfigPathForExecutable(wrapperPath), []byte(body), 0o600); err != nil {
		return fmt.Errorf("write codex debug wrapper config: %w", err)
	}
	return nil
}

type driverBackgroundProcess struct {
	process *os.Process
	stdin   *os.File
	done    chan struct{}
	exe     string
	args    []string
	created string
}

var driverBackgroundProcesses = struct {
	sync.Mutex
	byKey map[string]driverBackgroundProcess
}{byKey: map[string]driverBackgroundProcess{}}

var driverMCPStartupWait = 3 * time.Second

type DriverProcessMetadata struct {
	PID       int
	Exe       string
	Args      []string
	CreatedAt string
}

var inspectDriverProcess = inspectOSDriverProcess
var terminateDriverProcess = terminateOSDriverProcess

func driverProcessKey(exe, mode, configPath string) string {
	return exe + "\x00" + mode + "\x00" + configPath
}

func driverProcessMetadata(process *os.Process, exe string, args []string) DriverProcessMetadata {
	meta := DriverProcessMetadata{Exe: exe, Args: append([]string(nil), args...)}
	if process != nil {
		meta.PID = process.Pid
		if inspected, ok, err := inspectDriverProcess(process.Pid); err == nil && ok {
			meta.CreatedAt = inspected.CreatedAt
		}
	}
	return meta
}

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
	key := driverProcessKey(exe, "serve-mcp", configPath)
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
	args := []string{"serve-mcp", "--config", configPath}
	cmd := exec.Command(exe, args...)
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
	meta := driverProcessMetadata(cmd.Process, exe, args)
	entry := driverBackgroundProcess{
		process: cmd.Process,
		stdin:   stdinWriter,
		done:    done,
		exe:     meta.Exe,
		args:    meta.Args,
		created: meta.CreatedAt,
	}
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
		if entry.stdin != nil {
			_ = entry.stdin.Close()
		}
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
	_, err := StartDriverDaemonManaged(exe, configPath)
	return err
}

func StartDriverDaemonManaged(exe, configPath string) ([]DriverProcessMetadata, error) {
	if exe == "" || configPath == "" {
		return nil, nil
	}
	if _, err := os.Stat(exe); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	mcpErr := StartDriverMCPServer(exe, configPath)
	key := driverProcessKey(exe, "serve-daemon", configPath)
	driverBackgroundProcesses.Lock()
	if existing, ok := driverBackgroundProcesses.byKey[key]; ok {
		select {
		case <-existing.done:
			delete(driverBackgroundProcesses.byKey, key)
		default:
			driverBackgroundProcesses.Unlock()
			return trackedDriverMetadata(exe, configPath), mcpErr
		}
	}
	driverBackgroundProcesses.Unlock()

	args := []string{"serve-daemon", "--config", configPath}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	process.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	meta := driverProcessMetadata(cmd.Process, exe, args)
	entry := driverBackgroundProcess{
		process: cmd.Process,
		done:    done,
		exe:     meta.Exe,
		args:    meta.Args,
		created: meta.CreatedAt,
	}
	driverBackgroundProcesses.Lock()
	if existing, ok := driverBackgroundProcesses.byKey[key]; ok {
		driverBackgroundProcesses.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		select {
		case <-existing.done:
			return StartDriverDaemonManaged(exe, configPath)
		default:
			return trackedDriverMetadata(exe, configPath), mcpErr
		}
	}
	driverBackgroundProcesses.byKey[key] = entry
	driverBackgroundProcesses.Unlock()

	go func() {
		_ = cmd.Wait()
		driverBackgroundProcesses.Lock()
		if current, ok := driverBackgroundProcesses.byKey[key]; ok && current.done == done {
			delete(driverBackgroundProcesses.byKey, key)
		}
		driverBackgroundProcesses.Unlock()
		close(done)
	}()
	return trackedDriverMetadata(exe, configPath), mcpErr
}

func DriverDaemonRunning(exe, configPath string, persisted []DriverProcessMetadata) bool {
	key := driverProcessKey(exe, "serve-daemon", configPath)
	driverBackgroundProcesses.Lock()
	if existing, ok := driverBackgroundProcesses.byKey[key]; ok {
		select {
		case <-existing.done:
			delete(driverBackgroundProcesses.byKey, key)
		default:
			driverBackgroundProcesses.Unlock()
			return true
		}
	}
	driverBackgroundProcesses.Unlock()
	for _, meta := range persisted {
		if driverProcessMatches(exe, configPath, meta, "serve-daemon") {
			return true
		}
	}
	return false
}

func StopDriverDaemon(exe, configPath string, persisted []DriverProcessMetadata) error {
	var errs []error
	for _, mode := range []string{"serve-mcp", "serve-daemon"} {
		if err := stopTrackedDriverProcess(driverProcessKey(exe, mode, configPath)); err != nil {
			errs = append(errs, err)
		}
	}
	for _, meta := range persisted {
		mode, ok := driverProcessModeForConfig(configPath, meta)
		if !ok {
			continue
		}
		if mode != "serve-mcp" && mode != "serve-daemon" {
			continue
		}
		if !driverProcessMatches(exe, configPath, meta, mode) {
			continue
		}
		if !driverProcessMatches(exe, configPath, meta, mode) {
			continue
		}
		if err := terminateDriverProcess(context.Background(), meta.PID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func stopTrackedDriverProcess(key string) error {
	driverBackgroundProcesses.Lock()
	entry, ok := driverBackgroundProcesses.byKey[key]
	if ok {
		delete(driverBackgroundProcesses.byKey, key)
	}
	driverBackgroundProcesses.Unlock()
	if !ok {
		return nil
	}
	if entry.stdin != nil {
		_ = entry.stdin.Close()
	}
	if entry.process != nil {
		_ = entry.process.Kill()
	}
	select {
	case <-entry.done:
	case <-time.After(2 * time.Second):
	}
	return nil
}

func trackedDriverMetadata(exe, configPath string) []DriverProcessMetadata {
	keys := []string{
		driverProcessKey(exe, "serve-mcp", configPath),
		driverProcessKey(exe, "serve-daemon", configPath),
	}
	driverBackgroundProcesses.Lock()
	defer driverBackgroundProcesses.Unlock()
	var out []DriverProcessMetadata
	for _, key := range keys {
		entry, ok := driverBackgroundProcesses.byKey[key]
		if !ok || entry.process == nil {
			continue
		}
		select {
		case <-entry.done:
			delete(driverBackgroundProcesses.byKey, key)
			continue
		default:
		}
		out = append(out, DriverProcessMetadata{
			PID:       entry.process.Pid,
			Exe:       entry.exe,
			Args:      append([]string(nil), entry.args...),
			CreatedAt: entry.created,
		})
	}
	return out
}

func driverProcessMatches(exe, configPath string, persisted DriverProcessMetadata, mode string) bool {
	if persisted.PID <= 0 || persisted.CreatedAt == "" {
		return false
	}
	inspected, ok, err := inspectDriverProcess(persisted.PID)
	if err != nil || !ok {
		return false
	}
	if inspected.CreatedAt == "" || inspected.CreatedAt != persisted.CreatedAt {
		return false
	}
	if !samePath(inspected.Exe, exe) || !samePath(persisted.Exe, exe) {
		return false
	}
	if !driverArgsMatch(persisted.Args, mode, configPath) {
		return false
	}
	if len(inspected.Args) > 0 && !driverArgsMatch(inspected.Args, mode, configPath) {
		return false
	}
	return true
}

func driverProcessModeForConfig(configPath string, meta DriverProcessMetadata) (string, bool) {
	for _, mode := range []string{"serve-mcp", "serve-daemon"} {
		if driverArgsMatch(meta.Args, mode, configPath) {
			return mode, true
		}
	}
	return "", false
}

func driverArgsMatch(args []string, mode, configPath string) bool {
	if len(args) != 3 || args[0] != mode || args[1] != "--config" {
		return false
	}
	return samePath(args[2], configPath)
}

func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	aa, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func quote(v string) string {
	return strconv.Quote(v)
}

func quoteListSlash(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quote(filepath.ToSlash(value)))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
