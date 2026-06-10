package slave

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultObserverURL = "https://loom.nj.cs.ac.cn:10062/"

type ConfigInput struct {
	ServerURL   string
	ObserverURL string
	CodexBin    string
}

type loomSlaveConfig struct {
	Server      loomServer      `yaml:"server"`
	Credentials loomCredentials `yaml:"credentials"`
	Agent       loomAgent       `yaml:"agent"`
	Codex       loomCodex       `yaml:"codex"`
	Claude      loomClaude      `yaml:"claude"`
	Discovery   loomDiscovery   `yaml:"discovery"`
	Resources   loomResources   `yaml:"resources"`
	Observer    loomObserver    `yaml:"observer"`
}

type loomServer struct {
	URL  string `yaml:"url"`
	Name string `yaml:"name"`
}

type loomCredentials struct {
	SandboxID   string `yaml:"sandbox_id"`
	TunnelToken string `yaml:"tunnel_token"`
	ProxyToken  string `yaml:"proxy_token"`
	WorkspaceID string `yaml:"workspace_id"`
	ShortID     string `yaml:"short_id"`
}

type loomAgent struct {
	Kind string `yaml:"kind"`
}

type loomCodex struct {
	Bin       string   `yaml:"bin"`
	WorkDir   string   `yaml:"workdir"`
	ExtraArgs []string `yaml:"extra_args"`
}

type loomClaude struct {
	Bin       string   `yaml:"bin"`
	WorkDir   string   `yaml:"workdir"`
	ExtraArgs []string `yaml:"extra_args"`
}

type loomDiscovery struct {
	DisplayName string   `yaml:"display_name"`
	Description string   `yaml:"description"`
	Skills      []string `yaml:"skills"`
}

type loomResources struct {
	Tags []string `yaml:"tags"`
}

type loomObserver struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}

func WriteConfig(sl Slave, m Machine, in ConfigInput) error {
	if strings.TrimSpace(sl.DisplayName) == "" || strings.TrimSpace(sl.Folder) == "" || strings.TrimSpace(sl.ConfigPath) == "" {
		return fmt.Errorf("slave display name, folder, and config path required")
	}
	if strings.TrimSpace(m.MachineID) == "" || strings.TrimSpace(m.ComputerName) == "" {
		return fmt.Errorf("machine identity required")
	}
	serverURL := in.ServerURL
	if serverURL == "" {
		serverURL = "https://agent.cs.ac.cn"
	}
	observerURL := in.ObserverURL
	if observerURL == "" {
		observerURL = DefaultObserverURL
	}
	codexBin := in.CodexBin
	if codexBin == "" {
		codexBin = "codex"
	}
	cfg := loomSlaveConfig{
		Server: loomServer{URL: serverURL, Name: sl.DisplayName},
		Credentials: loomCredentials{
			SandboxID: "", TunnelToken: "", ProxyToken: "", WorkspaceID: "", ShortID: "",
		},
		Agent: loomAgent{Kind: "codex"},
		Codex: loomCodex{Bin: codexBin, WorkDir: sl.Folder, ExtraArgs: []string{}},
		Claude: loomClaude{
			Bin:       "",
			WorkDir:   sl.Folder,
			ExtraArgs: []string{},
		},
		Discovery: loomDiscovery{
			DisplayName: sl.DisplayName,
			Description: fmt.Sprintf("来自同一台电脑：%s；工作目录：%s", m.ComputerName, sl.Folder),
			// Loom filters unavailable shell skills at runtime, so Windows keeps PowerShell when Bash is absent.
			Skills: []string{"chat", "bash", "powershell", "file", "permissions", "register_mcp", "unregister_mcp"},
		},
		Resources: loomResources{Tags: []string{
			"agentserver-vscode-slave",
			"local-machine:" + m.MachineID,
			"host:" + m.ComputerName,
		}},
		Observer: loomObserver{Enabled: true, URL: observerURL},
	}
	if err := os.MkdirAll(filepath.Dir(sl.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("mkdir slave config dir: %w", err)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal slave config: %w", err)
	}
	return writeConfigFile(sl.ConfigPath, b)
}

func writeConfigFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create slave config temp: %w", err)
	}
	tmpPath := f.Name()
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	closed := false
	closeTemp := func() error {
		if closed {
			return nil
		}
		closed = true
		return f.Close()
	}

	if err := f.Chmod(0o600); err != nil {
		_ = closeTemp()
		return fmt.Errorf("chmod slave config temp: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = closeTemp()
		return fmt.Errorf("write slave config temp: %w", err)
	}
	if err := closeTemp(); err != nil {
		return fmt.Errorf("close slave config temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish slave config: %w", err)
	}
	tmpPath = ""
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod slave config: %w", err)
	}
	return nil
}
