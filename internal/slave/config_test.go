package slave

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWriteConfigPublishesMachineSourceInAgentserverCardFields(t *testing.T) {
	dir := t.TempDir()
	sl := Slave{
		DisplayName: "61414-PC-前端调试",
		Folder:      `C:\Users\61414\project-a`,
		ConfigPath:  filepath.Join(dir, "config.yaml"),
	}
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if err := WriteConfig(sl, m, ConfigInput{
		ServerURL:   "https://agent.cs.ac.cn",
		ObserverURL: "https://loom.nj.cs.ac.cn:10062/",
		CodexBin:    `C:\Users\61414\AppData\Local\agentserver-app\bin\codex.exe`,
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	cfg := readConfig(t, sl.ConfigPath)
	if cfg.Server.URL != "https://agent.cs.ac.cn" {
		t.Fatalf("server.url=%q", cfg.Server.URL)
	}
	if cfg.Server.Name != "61414-PC-前端调试" {
		t.Fatalf("server.name=%q", cfg.Server.Name)
	}
	if cfg.Agent.Kind != "codex" {
		t.Fatalf("agent.kind=%q", cfg.Agent.Kind)
	}
	if cfg.Codex.Bin != `C:\Users\61414\AppData\Local\agentserver-app\bin\codex.exe` {
		t.Fatalf("codex.bin=%q", cfg.Codex.Bin)
	}
	if cfg.Codex.WorkDir != `C:\Users\61414\project-a` {
		t.Fatalf("codex.workdir=%q", cfg.Codex.WorkDir)
	}
	if len(cfg.Codex.ExtraArgs) != 0 {
		t.Fatalf("codex.extra_args=%v", cfg.Codex.ExtraArgs)
	}
	if cfg.Claude.WorkDir != `C:\Users\61414\project-a` {
		t.Fatalf("claude.workdir=%q", cfg.Claude.WorkDir)
	}
	if len(cfg.Claude.ExtraArgs) != 0 {
		t.Fatalf("claude.extra_args=%v", cfg.Claude.ExtraArgs)
	}
	if cfg.Discovery.DisplayName != "61414-PC-前端调试" {
		t.Fatalf("discovery.display_name=%q", cfg.Discovery.DisplayName)
	}
	if cfg.Discovery.Description != `来自同一台电脑：61414-PC；工作目录：C:\Users\61414\project-a` {
		t.Fatalf("discovery.description=%q", cfg.Discovery.Description)
	}
	wantSkills := []string{"chat", "bash", "powershell", "file", "permissions", "register_mcp", "unregister_mcp"}
	if !slices.Equal(cfg.Discovery.Skills, wantSkills) {
		t.Fatalf("discovery.skills=%v", cfg.Discovery.Skills)
	}
	wantTags := []string{"agentserver-app-slave", "local-machine:machine-1", "host:61414-PC"}
	if !slices.Equal(cfg.Resources.Tags, wantTags) {
		t.Fatalf("resources.tags=%v", cfg.Resources.Tags)
	}
	if !cfg.Observer.Enabled {
		t.Fatal("observer.enabled=false")
	}
	if cfg.Observer.URL != "https://loom.nj.cs.ac.cn:10062/" {
		t.Fatalf("observer.url=%q", cfg.Observer.URL)
	}
}

func TestWriteConfigStartsWithoutCredentialsForReauth(t *testing.T) {
	dir := t.TempDir()
	sl := Slave{DisplayName: "PC-worker", Folder: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	m := Machine{MachineID: "machine-1", ComputerName: "PC"}

	if err := WriteConfig(sl, m, ConfigInput{ServerURL: "https://agent.cs.ac.cn"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	cfg := readConfig(t, sl.ConfigPath)
	if cfg.Credentials.SandboxID != "" {
		t.Fatalf("credentials.sandbox_id=%q", cfg.Credentials.SandboxID)
	}
	if cfg.Credentials.TunnelToken != "" {
		t.Fatalf("credentials.tunnel_token=%q", cfg.Credentials.TunnelToken)
	}
	if cfg.Credentials.ProxyToken != "" {
		t.Fatalf("credentials.proxy_token=%q", cfg.Credentials.ProxyToken)
	}
	if cfg.Credentials.WorkspaceID != "" {
		t.Fatalf("credentials.workspace_id=%q", cfg.Credentials.WorkspaceID)
	}
	if cfg.Credentials.ShortID != "" {
		t.Fatalf("credentials.short_id=%q", cfg.Credentials.ShortID)
	}
}

func TestWriteConfigDefaultsObserverURL(t *testing.T) {
	dir := t.TempDir()
	sl := Slave{DisplayName: "PC-worker", Folder: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	m := Machine{MachineID: "machine-1", ComputerName: "PC"}

	if err := WriteConfig(sl, m, ConfigInput{ServerURL: "https://agent.cs.ac.cn"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	cfg := readConfig(t, sl.ConfigPath)
	if cfg.Observer.URL != DefaultObserverURL {
		t.Fatalf("observer.url=%q, want %q", cfg.Observer.URL, DefaultObserverURL)
	}
}

func TestWriteConfigNarrowsExistingConfigFileMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	sl := Slave{DisplayName: "PC-worker", Folder: dir, ConfigPath: configPath}
	m := Machine{MachineID: "machine-1", ComputerName: "PC"}

	if err := WriteConfig(sl, m, ConfigInput{ServerURL: "https://agent.cs.ac.cn"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	readConfig(t, sl.ConfigPath)
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(sl.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config file mode=%#o, want 0600", got)
	}
}

func TestWriteConfigRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(sl *Slave, m *Machine)
	}{
		{
			name: "empty slave display name",
			mutate: func(sl *Slave, m *Machine) {
				sl.DisplayName = ""
			},
		},
		{
			name: "space-only slave display name",
			mutate: func(sl *Slave, m *Machine) {
				sl.DisplayName = "   "
			},
		},
		{
			name: "empty slave folder",
			mutate: func(sl *Slave, m *Machine) {
				sl.Folder = ""
			},
		},
		{
			name: "space-only slave folder",
			mutate: func(sl *Slave, m *Machine) {
				sl.Folder = "   "
			},
		},
		{
			name: "empty slave config path",
			mutate: func(sl *Slave, m *Machine) {
				sl.ConfigPath = ""
			},
		},
		{
			name: "space-only slave config path",
			mutate: func(sl *Slave, m *Machine) {
				sl.ConfigPath = "   "
			},
		},
		{
			name: "empty machine id",
			mutate: func(sl *Slave, m *Machine) {
				m.MachineID = ""
			},
		},
		{
			name: "space-only machine id",
			mutate: func(sl *Slave, m *Machine) {
				m.MachineID = "   "
			},
		},
		{
			name: "empty machine computer name",
			mutate: func(sl *Slave, m *Machine) {
				m.ComputerName = ""
			},
		},
		{
			name: "space-only machine computer name",
			mutate: func(sl *Slave, m *Machine) {
				m.ComputerName = "   "
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			sl := Slave{DisplayName: "PC-worker", Folder: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
			m := Machine{MachineID: "machine-1", ComputerName: "PC"}
			tt.mutate(&sl, &m)

			if err := WriteConfig(sl, m, ConfigInput{ServerURL: "https://agent.cs.ac.cn"}); err == nil {
				t.Fatal("expected invalid input error")
			}
		})
	}
}

func readConfig(t *testing.T, path string) parsedLoomConfig {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg parsedLoomConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("parse config: %v\n%s", err, b)
	}
	return cfg
}

type parsedLoomConfig struct {
	Server      parsedServer      `yaml:"server"`
	Credentials parsedCredentials `yaml:"credentials"`
	Agent       parsedAgent       `yaml:"agent"`
	Codex       parsedCodex       `yaml:"codex"`
	Claude      parsedClaude      `yaml:"claude"`
	Discovery   parsedDiscovery   `yaml:"discovery"`
	Resources   parsedResources   `yaml:"resources"`
	Observer    parsedObserver    `yaml:"observer"`
}

type parsedServer struct {
	URL  string `yaml:"url"`
	Name string `yaml:"name"`
}

type parsedCredentials struct {
	SandboxID   string `yaml:"sandbox_id"`
	TunnelToken string `yaml:"tunnel_token"`
	ProxyToken  string `yaml:"proxy_token"`
	WorkspaceID string `yaml:"workspace_id"`
	ShortID     string `yaml:"short_id"`
}

type parsedAgent struct {
	Kind string `yaml:"kind"`
}

type parsedCodex struct {
	Bin       string   `yaml:"bin"`
	WorkDir   string   `yaml:"workdir"`
	ExtraArgs []string `yaml:"extra_args"`
}

type parsedClaude struct {
	Bin       string   `yaml:"bin"`
	WorkDir   string   `yaml:"workdir"`
	ExtraArgs []string `yaml:"extra_args"`
}

type parsedDiscovery struct {
	DisplayName string   `yaml:"display_name"`
	Description string   `yaml:"description"`
	Skills      []string `yaml:"skills"`
}

type parsedResources struct {
	Tags []string `yaml:"tags"`
}

type parsedObserver struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}
