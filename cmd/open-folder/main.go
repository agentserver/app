// open-folder is invoked by the Explorer context menu with one argv: the
// folder path. It just execs VS Code with our user-data-dir + that folder.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/codex"
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
		log.Fatalf("VS Code path unknown — has onboarding run?")
	}
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		log.Fatal(err)
	}
	if exe, err := os.Executable(); err == nil {
		_ = tokenrefresh.StartDaemon(filepath.Join(filepath.Dir(exe), "token-refresher.exe"))
	}

	cmd := exec.Command(s.VSCode.Path, vscode.LaunchArgs(p.VSCodeUserDataDir, p.VSCodeExtDir, folder)...)
	sec := secrets.New(p.SecretsFile)
	if apiKey, err := sec.Get("modelserver_api_key"); err == nil {
		cmd.Env = vscode.UpsertEnv(os.Environ(), "OPENAI_API_KEY", apiKey)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened %s\n", folder)
}
