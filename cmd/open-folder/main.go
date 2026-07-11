// open-folder is invoked by the Explorer context menu with one argv: the
// folder path. It just execs VS Code with our user-data-dir + that folder.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/launchprep"
	"github.com/agentserver/agentserver-pkg/internal/modelaccess"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/opencode"
	"github.com/agentserver/agentserver-pkg/internal/opencodedesktop"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/process"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: open-folder <path>")
	}
	folder := os.Args[1]

	p, err := paths.Default()
	if err != nil {
		log.Fatal(err)
	}
	launcherExe := ""
	tokenRefresherExe := ""
	embeddedVSIXPath := ""
	installModePath := ""
	if exe, err := os.Executable(); err == nil {
		installDir := filepath.Dir(exe)
		launcherExe = filepath.Join(installDir, "launcher.exe")
		tokenRefresherExe = filepath.Join(installDir, "token-refresher.exe")
		embeddedVSIXPath = filepath.Join(installDir, "agentserver-app.vsix")
		installModePath = installmode.PathForExecutable(exe)
	}
	_ = ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
		LauncherExe: launcherExe,
		PortFile:    p.ConsolePortFile,
	})
	s, err := loadOpenFolderState(p, installModePath)
	if err != nil {
		log.Fatal(err)
	}
	switch state.NormalizeFrontendMode(s.FrontendMode) {
	case state.FrontendModeOpenCodeDesktop:
		if err := openFolderOpenCodeDesktop(context.Background(), p, folder, opencodedesktop.Detected{
			Installed: s.OpenCodeDesktop.Installed,
			Path:      s.OpenCodeDesktop.Path,
			Version:   s.OpenCodeDesktop.Version,
		}, tokenRefresherExe, nil); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("opened %s with OpenCode Desktop\n", folder)
		return
	case state.FrontendModeCodexDesktop:
		if err := openFolderCodexDesktop(context.Background(), p, folder, secrets.New(p.SecretsFile), tokenRefresherExe, nil); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("opened %s with %s\n", folder, codexdesktop.ShortDisplayName)
		return
	}
	if s.VSCode.Path == "" {
		log.Fatalf("VS Code path unknown - has onboarding run?")
	}
	if err := openFolder(context.Background(), s.VSCode.Path, p, folder, secrets.New(p.SecretsFile), tokenRefresherExe, embeddedVSIXPath); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened %s\n", folder)
}

func loadOpenFolderState(p paths.Paths, installModePath string) (*state.State, error) {
	store := state.NewStore(p.StateFile)
	if installModePath != "" {
		if err := installmode.SyncStoreIfPresent(store, installModePath); err != nil {
			return nil, err
		}
	}
	return store.Load()
}

type consoleBackgroundDeps struct {
	LauncherExe string
	PortFile    string
	Discover    func(context.Context, string) (console.InstanceInfo, bool)
	Start       func(string, ...string) error
}

func ensureConsoleBackground(ctx context.Context, d consoleBackgroundDeps) error {
	discover := d.Discover
	if discover == nil {
		discover = console.DiscoverInstance
	}
	if _, ok := discover(ctx, d.PortFile); ok {
		return nil
	}
	if d.LauncherExe == "" {
		return nil
	}
	start := d.Start
	if start == nil {
		start = startDetached
	}
	return start(d.LauncherExe, "--background")
}

func startDetached(exe string, args ...string) error {
	return startDetachedWithDeps(exe, args, process.HideWindow, func(cmd *exec.Cmd) error {
		return cmd.Start()
	})
}

func startDetachedWithDeps(exe string, args []string, hideWindow func(*exec.Cmd), start func(*exec.Cmd) error) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if hideWindow != nil {
		hideWindow(cmd)
	}
	return start(cmd)
}

func openFolder(ctx context.Context, codeExe string, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string, embeddedVSIXPath string) error {
	if err := launchprep.PrepareVSCode(ctx, launchprep.Input{
		CodeExe:          codeExe,
		Paths:            p,
		EmbeddedVSIXPath: embeddedVSIXPath,
	}); err != nil {
		return err
	}
	localProxyToken, err := localProxyBearerToken(p)
	if err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}

	cmd := exec.Command(codeExe, vscode.LaunchArgs(p.VSCodeUserDataDir, p.VSCodeExtDir, folder)...)
	cmd.Env = vscode.UpsertEnv(os.Environ(), codex.LocalProxyAPIKeyEnv, localProxyToken)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func openFolderCodexDesktop(ctx context.Context, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string, launcher codexdesktop.Launcher) error {
	localProxyToken, err := localProxyBearerToken(p)
	if err != nil {
		return err
	}
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, localProxyToken)); err != nil {
		return err
	}
	_ = env.PersistUserEnv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	_ = os.Setenv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	if err := codexdesktop.ConfigureLocale(
		p.CodexDesktopGlobalStateFile,
		p.CodexDesktopComputerUseConfigFile,
		codexdesktop.DefaultLocale,
	); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	if launcher == nil {
		launcher = codexdesktop.Launch
	}
	return launcher(ctx, folder)
}

func openFolderOpenCodeDesktop(ctx context.Context, p paths.Paths, folder string, det opencodedesktop.Detected, tokenRefresherExe string, launcher func(context.Context, opencodedesktop.LaunchOptions) error) error {
	localProxyToken, err := localProxyBearerToken(p)
	if err != nil {
		return err
	}
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, localProxyToken)); err != nil {
		return err
	}
	if p.OpenCodeConfigFile != "" {
		if err := opencode.UpdateConfig(p.OpenCodeConfigFile, opencode.Settings{
			BaseURL: modelproxy.DefaultBaseURL,
			Model:   "gpt-5.5",
		}); err != nil {
			return err
		}
	}
	_ = env.PersistUserEnv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	_ = env.PersistUserEnv(opencode.LocalProxyAPIKeyEnv, localProxyToken)
	_ = os.Setenv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	_ = os.Setenv(opencode.LocalProxyAPIKeyEnv, localProxyToken)
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	if launcher == nil {
		launcher = func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
			return opencodedesktop.Launch(ctx, opts)
		}
	}
	return launcher(ctx, opencodedesktop.LaunchOptions{
		Detected: det,
		Folder:   folder,
		Config: opencodedesktop.ConfigEnv{
			Path:      p.OpenCodeConfigFile,
			APIKeyEnv: opencode.LocalProxyAPIKeyEnv,
			APIKey:    localProxyToken,
		},
	})
}

func localProxyBearerToken(p paths.Paths) (string, error) {
	if p.InstallRoot == "" {
		return codex.LegacyLocalProxyAPIKeyValue, nil
	}
	return modelaccess.EnsureLocalProxyToken(modelaccess.DefaultLocalProxyTokenPath(p.InstallRoot))
}
