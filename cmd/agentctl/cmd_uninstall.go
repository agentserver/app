package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/uninstall"
)

func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	silent := fs.Bool("silent", false, "no prompts")
	removeVSCode := fs.Bool("vscode", false, "also uninstall VS Code")
	_ = fs.Parse(args)

	p, _ := paths.Default()
	if !*silent {
		fmt.Printf("This will remove %s shortcuts, context menu, state, and secrets.\n", branding.DisplayName)
		fmt.Print("Proceed? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if ans != "y" && ans != "Y" {
			fmt.Println("aborted")
			return
		}
	}

	sec := secrets.New(p.SecretsFile)
	appDir := ""
	if exe, err := os.Executable(); err == nil {
		appDir = filepath.Dir(exe)
	}
	if err := uninstall.Run(uninstall.Options{
		Paths:   p,
		Secrets: sec,
		AppDir:  appDir,
	}); err != nil {
		fmt.Println("warning:", err)
	}

	if *removeVSCode {
		fmt.Println("--vscode removal not implemented in v1; please remove manually via Apps & Features.")
	}
	fmt.Println("done.")
}
