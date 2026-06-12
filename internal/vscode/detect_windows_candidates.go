package vscode

import "strings"

func detectCandidatesWindows(localAppData, programFiles, programFilesX86 string) []string {
	var candidates []string
	if localAppData != "" {
		candidates = append(candidates,
			joinWindowsPath(localAppData, "Microsoft", "WindowsApps", "code.exe"),
			joinWindowsPath(localAppData, "Microsoft", "WindowsApps", "code.cmd"),
			joinWindowsPath(localAppData, "Programs", "Microsoft VS Code", "bin", "code.cmd"),
		)
	}
	if programFiles != "" {
		candidates = append(candidates, joinWindowsPath(programFiles, "Microsoft VS Code", "bin", "code.cmd"))
	}
	if programFilesX86 != "" {
		candidates = append(candidates, joinWindowsPath(programFilesX86, "Microsoft VS Code", "bin", "code.cmd"))
	}
	return candidates
}

func joinWindowsPath(root string, parts ...string) string {
	out := strings.TrimRight(root, `\/`)
	for _, part := range parts {
		out += `\` + strings.Trim(part, `\/`)
	}
	return out
}
