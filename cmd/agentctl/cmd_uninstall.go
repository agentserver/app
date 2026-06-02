package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
)

func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	silent := fs.Bool("silent", false, "no prompts")
	removeVSCode := fs.Bool("vscode", false, "also uninstall VS Code")
	_ = fs.Parse(args)

	p, _ := paths.Default()
	if !*silent {
		fmt.Println("This will remove agentserver-vscode shortcuts, context menu, state, and secrets.")
		fmt.Print("Proceed? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if ans != "y" && ans != "Y" {
			fmt.Println("aborted")
			return
		}
	}

	_ = shortcut.UninstallAll(shortcut.ContextMenuInput{RegistryKeySuffix: "AgentserverVscode"},
		"agentserver-vscode")
	sec := secrets.New(p.SecretsFile)
	_ = sec.Delete("modelserver_api_key")
	_ = sec.Delete("agentserver_ws_api_key")
	_ = os.RemoveAll(p.InstallRoot)
	_ = os.RemoveAll(p.LocalAppDataRoot)

	if *removeVSCode {
		fmt.Println("--vscode removal not implemented in v1; please remove manually via Apps & Features.")
	}
	fmt.Println("done.")
}
