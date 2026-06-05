package vscode

import "strings"

const Locale = "zh-cn"

func LaunchArgs(userDataDir, extensionsDir string, folders ...string) []string {
	args := []string{
		"--locale", Locale,
		"--user-data-dir", userDataDir,
		"--extensions-dir", extensionsDir,
	}
	for _, folder := range folders {
		if folder != "" {
			args = append(args, folder)
		}
	}
	return args
}

func UpsertEnv(base []string, key, value string) []string {
	if key == "" {
		return append([]string(nil), base...)
	}
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	replaced := false
	for _, item := range base {
		if strings.HasPrefix(item, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}
