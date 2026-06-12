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

func TestPathsIncludesSlaveManagementFiles(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.MachineFile != filepath.Join(p.InstallRoot, "machine.json") {
		t.Fatalf("MachineFile=%q", p.MachineFile)
	}
	if p.SlavesFile != filepath.Join(p.InstallRoot, "slaves.json") {
		t.Fatalf("SlavesFile=%q", p.SlavesFile)
	}
	if p.SlavesDir != filepath.Join(p.InstallRoot, "slaves") {
		t.Fatalf("SlavesDir=%q", p.SlavesDir)
	}
}

func TestDefaultIncludesUpdatePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.UpdateStateFile != filepath.Join(p.InstallRoot, "update-state.json") {
		t.Fatalf("UpdateStateFile=%q", p.UpdateStateFile)
	}
	if p.UpdatesCacheDir != filepath.Join(p.CacheDir, "updates") {
		t.Fatalf("UpdatesCacheDir=%q", p.UpdatesCacheDir)
	}
	if p.PendingSlaveRestartsFile != filepath.Join(p.InstallRoot, "pending-slave-restarts.json") {
		t.Fatalf("PendingSlaveRestartsFile=%q", p.PendingSlaveRestartsFile)
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
