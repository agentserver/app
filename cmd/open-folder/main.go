// open-folder is invoked by the Explorer context menu with one argv: the
// folder path. It just execs VS Code with our user-data-dir + that folder.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
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

	cmd := exec.Command(s.VSCode.Path,
		"--user-data-dir", p.VSCodeUserDataDir,
		"--extensions-dir", p.VSCodeExtDir,
		folder,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened %s\n", folder)
}
