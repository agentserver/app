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
	"agentserverApp.panel.allowed",
}

// WriteSettings merges agentserver-app defaults into path. Existing
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
		"locale":                                   "zh-cn",
		"telemetry.telemetryLevel":                 "off",
		"workbench.editor.languageDetection":       false,
		"workbench.startupEditor":                  "none",
		"workbench.activityBar.location":           "hidden",
		"workbench.statusBar.visible":              false,
		"workbench.panel.defaultLocation":          "bottom",
		"workbench.panel.opensMaximized":           "never",
		"workbench.panel.showLabels":               false,
		"workbench.view.alwaysShowHeaderActions":   false,
		"window.menuBarVisibility":                 "hidden",
		"window.commandCenter":                     false,
		"workbench.layoutControl.enabled":          false,
		"breadcrumbs.enabled":                      false,
		"editor.minimap.enabled":                   false,
		"editor.stickyScroll.enabled":              false,
		"workbench.editor.showTabs":                "single",
		"workbench.editor.empty.hint":              "hidden",
		"workbench.tips.enabled":                   false,
		"update.mode":                              "none",
		"update.enableWindowsBackgroundUpdates":    false,
		"update.showReleaseNotes":                  false,
		"update.showPostInstallInfo":               false,
		"extensions.autoCheckUpdates":              false,
		"extensions.autoUpdate":                    false,
		"extensions.ignoreRecommendations":         true,
		"explorer.openEditors.visible":             0,
		"workbench.localHistory.enabled":           false,
		"chat.disableAIFeatures":                   true,
		"chat.agent.enabled":                       false,
		"chat.agentHost.enabled":                   false,
		"chat.agentsControl.enabled":               false,
		"chat.agentSessionProjection.enabled":      false,
		"chat.titleBar.openInAgentsWindow.enabled": false,
		"chat.agentsHandoff.openInAgentsWindow":    false,
		"github.copilot.enable": map[string]any{
			"*": false,
		},
		"github.copilot.nextEditSuggestions.enabled":  false,
		"github.copilot.chat.reviewSelection.enabled": false,
		"github.copilot.chat.reviewAgent.enabled":     false,
		"github.copilot.chat.claudeAgent.enabled":     false,

		"agentserverApp.startup.openFolderIfEmpty": true,
		"agentserverApp.terminal.respawnOnClose":   true,
		"agentserverApp.terminal.profileName":      "codex",
		"agentserverApp.panel.hideViews": []string{
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
		"terminal.integrated.defaultProfile.osx": "codex",
		"terminal.integrated.profiles.osx": map[string]any{
			"codex": map[string]any{
				"path": "/bin/zsh",
				"args": []string{"-c", in.CodexAbsPath + "; exec /bin/zsh -l"},
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
