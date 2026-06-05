// Package shortcut creates the desktop .lnk and folder-context-menu
// integration. Windows is the only platform implemented in v1.
package shortcut

import "errors"

type DesktopInput struct {
	Name      string // e.g. "agentserver-vscode"
	TargetExe string // absolute path to launcher.exe
	Args      string // launcher takes none by default
	IconPath  string // absolute path to .ico
	WorkDir   string // working directory; "" → user home
}

type ContextMenuInput struct {
	MenuLabel         string // localized label, e.g. "用星池指挥官打开"
	HandlerExe        string // absolute path to open-folder.exe
	IconPath          string // absolute path to .ico
	RegistryKeySuffix string // e.g. "AgentserverVscode"
}

func EnsureDesktopShortcut(in DesktopInput) error {
	if in.Name == "" || in.TargetExe == "" {
		return errors.New("EnsureDesktopShortcut: Name and TargetExe required")
	}
	return ensureDesktopShortcutPlatform(in)
}

func InstallContextMenu(in ContextMenuInput) error {
	if in.MenuLabel == "" || in.HandlerExe == "" || in.RegistryKeySuffix == "" {
		return errors.New("InstallContextMenu: MenuLabel/HandlerExe/RegistryKeySuffix required")
	}
	return installContextMenuPlatform(in)
}

func UninstallAll(in ContextMenuInput, desktopName string) error {
	return uninstallAllPlatform(in, desktopName)
}
