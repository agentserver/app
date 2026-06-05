//go:build windows

package shortcut

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestEnsureDesktopShortcut_Windows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := DesktopInput{
		Name:      "agentserver-vscode-test",
		TargetExe: `C:\Windows\System32\notepad.exe`,
		IconPath:  `C:\Windows\System32\notepad.exe`,
		WorkDir:   dir,
	}
	if err := EnsureDesktopShortcut(in); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "Desktop", "agentserver-vscode-test.lnk")
	if _, err := os.Stat(link); err != nil {
		t.Errorf("expected .lnk at %s", link)
	}
}

func TestInstallContextMenu_Windows(t *testing.T) {
	in := ContextMenuInput{
		MenuLabel:         "Test menu label",
		HandlerExe:        `C:\Windows\System32\notepad.exe`,
		IconPath:          `C:\Windows\System32\notepad.exe`,
		RegistryKeySuffix: "AgentserverVscodeTest",
	}
	if err := InstallContextMenu(in); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Cleanup
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\*\shell\AgentserverVscodeTest\command`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\*\shell\AgentserverVscodeTest`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\shell\AgentserverVscodeTest\command`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\shell\AgentserverVscodeTest`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\Background\shell\AgentserverVscodeTest\command`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\Background\shell\AgentserverVscodeTest`)
	}()
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\Directory\shell\AgentserverVscodeTest`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	label, _, _ := k.GetStringValue("")
	if label != "Test menu label" {
		t.Errorf("label %q", label)
	}

	fileCmd, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\*\shell\AgentserverVscodeTest\command`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer fileCmd.Close()
	cmd, _, _ := fileCmd.GetStringValue("")
	if cmd != `"C:\Windows\System32\notepad.exe" "%1"` {
		t.Errorf("file command %q", cmd)
	}
}
