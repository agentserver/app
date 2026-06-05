package vscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type SettingsInput struct {
	CodexAbsPath string // absolute path to codex.exe
}

var retiredManagedKeys = []string{
	"agentserverVscode.panel.allowed",
}

// WriteSettings merges agentserver-vscode defaults into path. Existing
// user keys not managed by us are preserved.
func WriteSettings(path string, in SettingsInput) error {
	if in.CodexAbsPath == "" {
		return fmt.Errorf("WriteSettings: CodexAbsPath required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse existing settings.json: %w", err)
		}
	}
	overrides := map[string]any{
		"locale":                                 "zh-cn",
		"telemetry.telemetryLevel":               "off",
		"workbench.editor.languageDetection":     false,
		"workbench.startupEditor":                "none",
		"workbench.activityBar.location":         "hidden",
		"workbench.statusBar.visible":            false,
		"workbench.panel.defaultLocation":        "bottom",
		"workbench.panel.opensMaximized":         "never",
		"workbench.panel.showLabels":             false,
		"workbench.view.alwaysShowHeaderActions": false,
		"window.menuBarVisibility":               "hidden",
		"window.commandCenter":                   false,
		"workbench.layoutControl.enabled":        false,
		"breadcrumbs.enabled":                    false,
		"editor.minimap.enabled":                 false,
		"editor.stickyScroll.enabled":            false,
		"workbench.editor.showTabs":              "single",
		"workbench.editor.empty.hint":            "hidden",
		"workbench.tips.enabled":                 false,
		"update.showReleaseNotes":                false,
		"extensions.ignoreRecommendations":       true,
		"explorer.openEditors.visible":           0,
		"workbench.localHistory.enabled":         false,

		"agentserverVscode.startup.openFolderIfEmpty": true,
		"agentserverVscode.terminal.respawnOnClose":   true,
		"agentserverVscode.terminal.profileName":      "codex",
		"agentserverVscode.panel.hideViews": []string{
			"workbench.panel.markers.view",
			"workbench.panel.output",
			"workbench.panel.repl.view",
			"workbench.panel.comments",
			"~remote.forwardedPorts",
			"workbench.panel.testResults",
			"outline",
			"timeline",
		},

		"terminal.integrated.defaultProfile.windows": "codex",
		"terminal.integrated.profiles.windows": map[string]any{
			"codex": map[string]any{
				"path": `C:\Windows\System32\cmd.exe`,
				"args": []string{"/k", in.CodexAbsPath},
			},
		},
	}
	for k, v := range overrides {
		m[k] = v
	}
	for _, k := range retiredManagedKeys {
		delete(m, k)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
