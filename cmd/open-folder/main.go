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
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/launchprep"
	"github.com/agentserver/agentserver-pkg/internal/paths"
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
	tokenRefresherExe := ""
	embeddedVSIXPath := ""
	installModePath := ""
	if exe, err := os.Executable(); err == nil {
		installDir := filepath.Dir(exe)
		tokenRefresherExe = filepath.Join(installDir, "token-refresher.exe")
		embeddedVSIXPath = filepath.Join(installDir, "agentserver-vscode.vsix")
		installModePath = installmode.PathForExecutable(exe)
	}
	s, err := loadOpenFolderState(p, installModePath)
	if err != nil {
		log.Fatal(err)
	}
	if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeCodexDesktop {
		if err := openFolderCodexDesktop(context.Background(), p, folder, secrets.New(p.SecretsFile), tokenRefresherExe, nil); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("opened %s with Codex Desktop\n", folder)
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

func openFolder(ctx context.Context, codeExe string, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string, embeddedVSIXPath string) error {
	if err := launchprep.PrepareVSCode(ctx, launchprep.Input{
		CodeExe:          codeExe,
		Paths:            p,
		EmbeddedVSIXPath: embeddedVSIXPath,
	}); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}

	cmd := exec.Command(codeExe, vscode.LaunchArgs(p.VSCodeUserDataDir, p.VSCodeExtDir, folder)...)
	if sec != nil {
		if apiKey, err := sec.Get("modelserver_api_key"); err == nil {
			cmd.Env = vscode.UpsertEnv(os.Environ(), "OPENAI_API_KEY", apiKey)
		}
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func openFolderCodexDesktop(ctx context.Context, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string, opener codexdesktop.Opener) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	return codexdesktop.Launch(ctx, folder, opener)
}
