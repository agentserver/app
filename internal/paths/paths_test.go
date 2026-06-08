package paths

import (
	"path/filepath"
	"testing"
)

func TestPathsConsistent(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.UserHome == "" {
		t.Errorf("UserHome empty")
	}
	if p.StateFile == "" || p.SecretsFile == "" {
		t.Errorf("missing state/secrets path")
	}
	if p.CacheDir == "" {
		t.Errorf("missing cache dir")
	}
}

func TestPathsIncludesConsoleRuntimeFiles(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.ConsolePortFile == "" {
		t.Fatal("ConsolePortFile empty")
	}
	if p.ConsoleNotificationsFile == "" {
		t.Fatal("ConsoleNotificationsFile empty")
	}
}

func TestPathsIncludesCodexDesktopLocaleFiles(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.CodexDesktopGlobalStateFile != filepath.Join(p.CodexDir, ".codex-global-state.json") {
		t.Fatalf("CodexDesktopGlobalStateFile=%q", p.CodexDesktopGlobalStateFile)
	}
	if p.CodexDesktopComputerUseConfigFile != filepath.Join(p.CodexDir, "computer-use", "config.json") {
		t.Fatalf("CodexDesktopComputerUseConfigFile=%q", p.CodexDesktopComputerUseConfigFile)
	}
}
