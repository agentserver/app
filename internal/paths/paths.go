// Package paths centralizes all on-disk locations.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	UserHome string

	// Per-install state root (~/.agentserver-vscode/)
	InstallRoot              string
	StateFile                string
	SecretsFile              string
	CacheDir                 string
	ConsolePortFile          string
	ConsoleNotificationsFile string
	VSCodeUserDataDir        string
	VSCodeExtDir             string

	// Codex config
	CodexDir                          string
	CodexConfigFile                   string
	CodexDesktopGlobalStateFile       string
	CodexDesktopComputerUseConfigFile string

	// LocalAppData root (Windows) for binaries
	LocalAppDataRoot string
	CodexExePath     string
}

func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("user home: %w", err)
	}
	root := filepath.Join(home, ".agentserver-vscode")
	codex := filepath.Join(home, ".codex")
	p := Paths{
		UserHome:                          home,
		InstallRoot:                       root,
		StateFile:                         filepath.Join(root, "state.json"),
		SecretsFile:                       filepath.Join(root, "secrets.json"),
		CacheDir:                          filepath.Join(root, "cache"),
		ConsolePortFile:                   filepath.Join(root, "console-port.json"),
		ConsoleNotificationsFile:          filepath.Join(root, "console-notifications.json"),
		VSCodeUserDataDir:                 filepath.Join(root, "vscode-data"),
		VSCodeExtDir:                      filepath.Join(root, "vscode-extensions"),
		CodexDir:                          codex,
		CodexConfigFile:                   filepath.Join(codex, "config.toml"),
		CodexDesktopGlobalStateFile:       filepath.Join(codex, ".codex-global-state.json"),
		CodexDesktopComputerUseConfigFile: filepath.Join(codex, "computer-use", "config.json"),
	}
	switch runtime.GOOS {
	case "windows":
		lad := os.Getenv("LOCALAPPDATA")
		if lad == "" {
			lad = filepath.Join(home, "AppData", "Local")
		}
		p.LocalAppDataRoot = filepath.Join(lad, "agentserver-vscode")
		p.CodexExePath = filepath.Join(p.LocalAppDataRoot, "bin", "codex.exe")
	default:
		p.LocalAppDataRoot = filepath.Join(root, "bin-root")
		p.CodexExePath = filepath.Join(p.LocalAppDataRoot, "bin", "codex")
	}
	return p, nil
}
