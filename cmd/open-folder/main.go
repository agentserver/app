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
	s, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		log.Fatal(err)
	}
	if s.VSCode.Path == "" {
		log.Fatalf("VS Code path unknown - has onboarding run?")
	}
	tokenRefresherExe := ""
	embeddedVSIXPath := ""
	if exe, err := os.Executable(); err == nil {
		installDir := filepath.Dir(exe)
		tokenRefresherExe = filepath.Join(installDir, "token-refresher.exe")
		embeddedVSIXPath = filepath.Join(installDir, "agentserver-vscode.vsix")
	}
	if err := openFolder(context.Background(), s.VSCode.Path, p, folder, secrets.New(p.SecretsFile), tokenRefresherExe, embeddedVSIXPath); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened %s\n", folder)
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
