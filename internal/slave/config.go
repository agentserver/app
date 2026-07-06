package slave

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultServerURL = "https://agent.cs.ac.cn"
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
	Kind      string   `yaml:"kind"`
	Bin       string   `yaml:"bin"`
	WorkDir   string   `yaml:"workdir"`
	CodexHome string   `yaml:"codex_home,omitempty"`
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
		serverURL = DefaultServerURL
	}
	observerURL := in.ObserverURL
	if observerURL == "" {
		observerURL = DefaultObserverURL
	}
	codexBin := in.CodexBin
	if codexBin == "" {
		codexBin = "codex"
	}
	codexHome := slaveCodexHome(sl.Folder)
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		return fmt.Errorf("mkdir slave codex home: %w", err)
	}
	credentials := loomCredentials{
		SandboxID: "", TunnelToken: "", ProxyToken: "", WorkspaceID: "", ShortID: "",
	}
	if existing, ok := existingCredentials(sl.ConfigPath); ok {
		credentials = existing
	}
	cfg := loomSlaveConfig{
		Server:      loomServer{URL: serverURL, Name: sl.DisplayName},
		Credentials: credentials,
		Agent:       loomAgent{Kind: "codex", Bin: codexBin, WorkDir: sl.Folder, CodexHome: codexHome, ExtraArgs: []string{}},
		Discovery: loomDiscovery{
			DisplayName: sl.DisplayName,
			Description: fmt.Sprintf("来自同一台电脑：%s；工作目录：%s", m.ComputerName, sl.Folder),
			// Loom filters unavailable shell skills at runtime, so Windows keeps PowerShell when Bash is absent.
			Skills: []string{"chat", "bash", "powershell", "file", "permissions", "register_mcp", "unregister_mcp"},
		},
		Resources: loomResources{Tags: []string{
			"agentserver-app-slave",
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

func slaveCodexHome(folder string) string {
	return filepath.Join(folder, ".codex")
}

func existingCredentials(path string) (loomCredentials, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return loomCredentials{}, false
	}
	var cfg struct {
		Credentials loomCredentials `yaml:"credentials"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return loomCredentials{}, false
	}
	c := cfg.Credentials
	if strings.TrimSpace(c.SandboxID) == "" &&
		strings.TrimSpace(c.TunnelToken) == "" &&
		strings.TrimSpace(c.ProxyToken) == "" &&
		strings.TrimSpace(c.WorkspaceID) == "" &&
		strings.TrimSpace(c.ShortID) == "" {
		return loomCredentials{}, false
	}
	return c, true
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
	if err := f.Sync(); err != nil {
		_ = closeTemp()
		return fmt.Errorf("sync slave config temp: %w", err)
	}
	if err := closeTemp(); err != nil {
		return fmt.Errorf("close slave config temp: %w", err)
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("publish slave config: %w", err)
	}
	tmpPath = ""
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod slave config: %w", err)
	}
	return nil
}
