package codexdesktop

import (
	"os"
	"strings"
)

func DefaultCLIPath(localAppData string) string {
	return defaultCLIPath(localAppData, fileExists)
}

func defaultCLIPath(localAppData string, exists func(string) bool) string {
	localAppData = strings.TrimRight(strings.TrimSpace(localAppData), `\/`)
	if localAppData == "" {
		return ""
	}
	candidate := joinWindowsPath(localAppData, "Microsoft", "WindowsApps", "codex.exe")
	if exists != nil && exists(candidate) {
		return candidate
	}
	return ""
}

func joinWindowsPath(root string, parts ...string) string {
	out := strings.TrimRight(strings.TrimSpace(root), `\/`)
	for _, part := range parts {
		out += `\` + strings.Trim(part, `\/`)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
