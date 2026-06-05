//go:build windows

package shortcut

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func ensureDesktopShortcutPlatform(in DesktopInput) error {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		return err
	}
	linkPath := filepath.Join(desktop, in.Name+".lnk")
	return createShellLink(linkPath, in.TargetExe, in.Args, in.IconPath, in.WorkDir)
}

// createShellLink shells out to PowerShell + WScript.Shell to create a .lnk.
// PowerShell is present on Windows 7+; this avoids the need for a Go COM library.
func createShellLink(linkPath, target, args, icon, workdir string) error {
	// Build a small PowerShell script. Quote all values via single quotes;
	// escape embedded single quotes by doubling them.
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

	var sb strings.Builder
	sb.WriteString(`$w = New-Object -ComObject WScript.Shell;`)
	sb.WriteString(`$s = $w.CreateShortcut(` + q(linkPath) + `);`)
	sb.WriteString(`$s.TargetPath = ` + q(target) + `;`)
	if args != "" {
		sb.WriteString(`$s.Arguments = ` + q(args) + `;`)
	}
	if workdir != "" {
		sb.WriteString(`$s.WorkingDirectory = ` + q(workdir) + `;`)
	}
	if icon != "" {
		sb.WriteString(`$s.IconLocation = ` + q(icon+",0") + `;`)
	}
	sb.WriteString(`$s.Save();`)
	sb.WriteString(`if (-not (Test-Path ` + q(linkPath) + `)) { exit 1 }`)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", sb.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell create-shortcut: %w (%s)", err, out)
	}
	return nil
}

func installContextMenuPlatform(in ContextMenuInput) error {
	for _, entry := range []struct {
		base     string
		argToken string
	}{
		{`Software\Classes\*\shell\` + in.RegistryKeySuffix, `%1`},
		{`Software\Classes\Directory\shell\` + in.RegistryKeySuffix, `%V`},
		{`Software\Classes\Directory\Background\shell\` + in.RegistryKeySuffix, `%V`},
	} {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, entry.base, registry.ALL_ACCESS)
		if err != nil {
			return fmt.Errorf("create %s: %w", entry.base, err)
		}
		if err := k.SetStringValue("", in.MenuLabel); err != nil {
			k.Close()
			return err
		}
		if in.IconPath != "" {
			_ = k.SetStringValue("Icon", in.IconPath)
		}
		k.Close()

		cmdKey := entry.base + `\command`
		k2, _, err := registry.CreateKey(registry.CURRENT_USER, cmdKey, registry.ALL_ACCESS)
		if err != nil {
			return fmt.Errorf("create %s: %w", cmdKey, err)
		}
		cmd := fmt.Sprintf(`"%s" "%s"`, in.HandlerExe, entry.argToken)
		if err := k2.SetStringValue("", cmd); err != nil {
			k2.Close()
			return err
		}
		k2.Close()
	}
	return nil
}

func uninstallAllPlatform(in ContextMenuInput, desktopName string) error {
	for _, base := range []string{
		`Software\Classes\*\shell\` + in.RegistryKeySuffix + `\command`,
		`Software\Classes\*\shell\` + in.RegistryKeySuffix,
		`Software\Classes\Directory\shell\` + in.RegistryKeySuffix + `\command`,
		`Software\Classes\Directory\shell\` + in.RegistryKeySuffix,
		`Software\Classes\Directory\Background\shell\` + in.RegistryKeySuffix + `\command`,
		`Software\Classes\Directory\Background\shell\` + in.RegistryKeySuffix,
	} {
		_ = registry.DeleteKey(registry.CURRENT_USER, base)
	}
	if desktopName != "" {
		link := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", desktopName+".lnk")
		_ = os.Remove(link)
	}
	return nil
}
