package launchprep

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

type Input struct {
	CodeExe          string
	Paths            paths.Paths
	EmbeddedVSIXPath string
}

var installExtensions = vscode.InstallExtensions

func PrepareVSCode(ctx context.Context, in Input) error {
	settingsPath := filepath.Join(in.Paths.VSCodeUserDataDir, "User", "settings.json")
	if err := vscode.WriteSettings(settingsPath, vscode.SettingsInput{
		CodexAbsPath: in.Paths.CodexExePath,
	}); err != nil {
		return err
	}
	if err := vscode.EnsureTerminalOnlyPanelState(in.Paths.VSCodeUserDataDir); err != nil {
		return fmt.Errorf("prepare VS Code panel state: %w", err)
	}
	if err := codex.UpdateConfig(in.Paths.CodexConfigFile, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, codex.LegacyLocalProxyAPIKeyValue)); err != nil {
		return err
	}
	if in.EmbeddedVSIXPath == "" {
		return nil
	}
	if err := installExtensions(ctx, vscode.Installer{
		CodeExe:       in.CodeExe,
		UserDataDir:   in.Paths.VSCodeUserDataDir,
		ExtensionsDir: in.Paths.VSCodeExtDir,
		Extensions:    []string{in.EmbeddedVSIXPath},
	}); err != nil {
		return fmt.Errorf("refresh VS Code extension: %w", err)
	}
	return nil
}
